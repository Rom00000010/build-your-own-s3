package main

import (
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 启停测试策略：从外部启动程序，检查固定监听地址、请求响应和 data 目录；
// 再发送 SIGINT，检查程序正常退出且停止监听。
func TestServiceStartsAndStops(t *testing.T) {
	buildDir := t.TempDir()
	goCache := "GOCACHE=" + filepath.Join(buildDir, "go-cache")
	binary := filepath.Join(buildDir, "server")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), goCache)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建服务程序失败：%v\n%s", err, output)
	}

	const address = "127.0.0.1:8080"
	if conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatalf("%s 已被占用，无法验证服务实际监听地址", address)
	}

	workDir := t.TempDir()
	logFile, err := os.Create(filepath.Join(workDir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	cmd := exec.Command(binary)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动服务失败：%v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	running := true
	t.Cleanup(func() {
		if running {
			_ = cmd.Process.Kill()
			<-wait
		}
	})

	client := &http.Client{Timeout: 200 * time.Millisecond}
	url := "http://" + address + "/startup-check-missing-bucket"
	deadline := time.Now().Add(5 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("读取服务响应失败：%v", readErr)
			}
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("启动后请求返回 HTTP %d，期望 404；响应体 %q", resp.StatusCode, body)
			}
			mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				t.Fatalf("Content-Type = %q，期望 application/json", resp.Header.Get("Content-Type"))
			}
			var result struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &result); err != nil || result.Error != "BucketNotExist" {
				t.Fatalf("错误响应 = %q，期望 BucketNotExist (%v)", body, err)
			}
			if info, err := os.Stat(filepath.Join(workDir, "data")); err != nil || !info.IsDir() {
				t.Fatalf("服务未在当前工作目录创建 data 目录：%v", err)
			}
			started = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !started {
		logs, _ := os.ReadFile(logFile.Name())
		t.Fatalf("服务未在 %s 响应；启动日志：%s", address, logs)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("发送 SIGINT 失败：%v", err)
	}
	select {
	case err := <-wait:
		running = false
		if err != nil {
			t.Fatalf("正常停止返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT 后服务未正常退出")
	}
	if conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatalf("停止后 %s 仍在监听", address)
	}
}
