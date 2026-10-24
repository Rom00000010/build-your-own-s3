package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
)

type historyInput struct {
	kind  string
	value string
}

type historyOutput struct {
	status int
	body   string
}

type historyResult struct {
	operation porcupine.Operation
	err       error
}

func singleObjectModel() porcupine.Model {
	return porcupine.Model{
		Init: func() any { return "" },
		Step: func(state, input, output any) (bool, any) {
			value := state.(string)
			in := input.(historyInput)
			out := output.(historyOutput)
			switch in.kind {
			case "put":
				return out.status == http.StatusNoContent && out.body == "", in.value
			case "get":
				if value == "" {
					var body struct {
						Error string `json:"error"`
					}
					ok := out.status == http.StatusNotFound && json.Unmarshal([]byte(out.body), &body) == nil && body.Error == "ObjectNotExist"
					return ok, value
				}
				return out.status == http.StatusOK && out.body == value, value
			case "delete":
				return out.status == http.StatusNoContent && out.body == "", ""
			case "list":
				var body struct {
					Keys []string `json:"keys"`
				}
				if out.status != http.StatusOK || json.Unmarshal([]byte(out.body), &body) != nil {
					return false, value
				}
				if value == "" {
					return len(body.Keys) == 0, value
				}
				return len(body.Keys) == 1 && body.Keys[0] == "a", value
			}
			return false, state
		},
	}
}

func historyRequest(baseURL, bucket string, in historyInput) (*http.Request, error) {
	objectURL := baseURL + "/" + bucket + "/a"
	switch in.kind {
	case "put":
		return http.NewRequest(http.MethodPut, objectURL, strings.NewReader(in.value))
	case "get":
		return http.NewRequest(http.MethodGet, objectURL, nil)
	case "delete":
		return http.NewRequest(http.MethodDelete, objectURL, nil)
	case "list":
		return http.NewRequest(http.MethodGet, baseURL+"/"+bucket, nil)
	}
	return nil, fmt.Errorf("unknown operation %q", in.kind)
}

func recordHistoryRequest(client *http.Client, req *http.Request, in historyInput, clientID int, origin time.Time, start <-chan struct{}) <-chan historyResult {
	done := make(chan historyResult, 1)
	go func() {
		<-start
		call := time.Since(origin).Nanoseconds()
		response := completeRequest(client, req)
		ret := time.Since(origin).Nanoseconds()
		done <- historyResult{operation: porcupine.Operation{
			ClientId: clientID, Input: in, Call: call,
			Output: historyOutput{status: response.status, body: string(response.body)}, Return: ret,
		}, err: response.err}
	}()
	return done
}

func TestSingleObjectModelRejectsStaleList(t *testing.T) {
	history := []porcupine.Operation{
		{Input: historyInput{kind: "put", value: "A"}, Output: historyOutput{status: http.StatusNoContent}, Call: 1, Return: 2},
		{Input: historyInput{kind: "list"}, Output: historyOutput{status: http.StatusOK, body: `{"keys":[]}`}, Call: 3, Return: 4},
	}
	if porcupine.CheckOperations(singleObjectModel(), history) {
		t.Fatal("模型接受了上传完成后仍返回空列表的历史")
	}
}

// 历史测试策略：固定一个已存在的桶和一个 key，轮换上传、读取、删除、列举的组合；
// 部分轮次暂停上传，在它未完成时执行其他请求。记录调用与完整响应后检查整段历史。
func TestHTTPObjectHistoryLinearizable(t *testing.T) {
	server := testHTTPServer(t)
	client := concurrentClient(server)
	model := singleObjectModel()
	patterns := [][][]historyInput{
		{
			{{kind: "put", value: "A"}, {kind: "get"}, {kind: "list"}, {kind: "delete"}},
			{{kind: "put", value: "B"}, {kind: "get"}, {kind: "list"}, {kind: "delete"}},
		},
		{
			{{kind: "put", value: "A"}, {kind: "put", value: "B"}, {kind: "get"}, {kind: "list"}},
			{{kind: "delete"}, {kind: "get"}, {kind: "list"}, {kind: "put", value: "C"}},
		},
		{
			{{kind: "delete"}, {kind: "get"}, {kind: "list"}, {kind: "put", value: "A"}},
			{{kind: "get"}, {kind: "list"}, {kind: "put", value: "B"}, {kind: "delete"}},
		},
	}
	for round := range 10 {
		bucket := fmt.Sprintf("history-%d", round)
		requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/"+bucket, nil)), http.StatusCreated, "")
		origin := time.Now()
		history := make([]porcupine.Operation, 0, 16)
		nextID := 0
		addResult := func(done <-chan historyResult) {
			got := <-done
			if got.err != nil {
				t.Fatalf("第 %d 轮 HTTP 请求失败：%v", round, got.err)
			}
			history = append(history, got.operation)
		}
		runWave := func(wave []historyInput) {
			results := make([]<-chan historyResult, 0, len(wave))
			start := make(chan struct{})
			for _, in := range wave {
				req, err := historyRequest(server.URL, bucket, in)
				if err != nil {
					t.Fatal(err)
				}
				results = append(results, recordHistoryRequest(client, req, in, nextID, origin, start))
				nextID++
			}
			close(start)
			for _, result := range results {
				addResult(result)
			}
		}
		for _, wave := range patterns[round%len(patterns)] {
			runWave(wave)
		}
		if round%3 == 0 {
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close() })
			req, err := http.NewRequest(http.MethodPut, server.URL+"/"+bucket+"/a", reader)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			req.Header.Set("Expect", "100-continue")
			req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
				Got100Continue: func() { close(started) },
			}))
			start := make(chan struct{})
			close(start)
			putDone := recordHistoryRequest(client, req, historyInput{kind: "put", value: "CD"}, nextID, origin, start)
			nextID++
			select {
			case <-started:
			case got := <-putDone:
				t.Fatalf("上传尚未开始读取请求体就已结束：%+v", got)
			case <-time.After(5 * time.Second):
				t.Fatal("上传未开始读取请求体")
			}
			if _, err := writer.Write([]byte("C")); err != nil {
				t.Fatal(err)
			}
			runWave([]historyInput{{kind: "get"}, {kind: "list"}, {kind: "delete"}})
			if _, err := writer.Write([]byte("D")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			addResult(putDone)
		}
		runWave([]historyInput{{kind: "get"}, {kind: "list"}})
		if !porcupine.CheckOperations(model, history) {
			t.Fatalf("第 %d 轮历史不满足线性一致性：%+v", round, history)
		}
	}
}
