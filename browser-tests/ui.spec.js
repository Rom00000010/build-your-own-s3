const { test, expect } = require('@playwright/test');
const { createServer } = require('node:http');
const { readFile } = require('node:fs/promises');
const path = require('node:path');

// 测试策略：
// 1. 常规交互：列表按钮打开表单对话框，取消不写入、成功后关闭；删除通过页面内对话框确认。
// 2. 前端输入：桶名/key 按合法边界与非法分区取代表，检查编辑时的就地提示及清空后的消除；空文件和取消操作不发送请求。
// 3. 下载：分别覆盖支持保存位置选择、取消选择及不支持时的常规下载。
// 4. HTTP 结果：模拟页面可遇到的重复桶、非空桶、写请求失败和写成功后列表刷新失败，检查提示与表单状态。
// 接口状态码、JSON 细节及页面无法触发的“对象不存在”由 Go HTTP 测试覆盖。

let server;
let origin;

test.beforeAll(async () => {
  server = createServer(async (req, res) => {
    const files = {
      '/_ui/': ['index.html', 'text/html'],
      '/_ui/style.css': ['style.css', 'text/css'],
      '/_ui/app.js': ['app.js', 'text/javascript'],
      '/_ui/bucket.svg': ['bucket.svg', 'image/svg+xml'],
    };
    const file = files[new URL(req.url, 'http://localhost').pathname];
    if (!file) {
      res.writeHead(404).end();
      return;
    }
    try {
      const body = await readFile(path.join(__dirname, '..', 'web', file[0]));
      res.writeHead(200, { 'Content-Type': file[1] }).end(body);
    } catch {
      res.writeHead(404).end();
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  origin = `http://127.0.0.1:${server.address().port}`;
});

test.afterAll(async () => {
  await new Promise(resolve => server.close(resolve));
});

async function mockAPI(page, initial = {}) {
  const buckets = new Map(Object.entries(initial).map(([name, objects]) => [name, new Map(Object.entries(objects))]));
  const requests = [];
  await page.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.startsWith('/_ui/') || url.pathname === '/favicon.ico') {
      await route.continue();
      return;
    }
    const method = request.method();
    const parts = url.pathname.split('/').filter(Boolean).map(decodeURIComponent);
    requests.push({ method, path: url.pathname });
    const json = (status, value) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) });
    const error = (status, name) => json(status, { error: name });
    if (method === 'GET' && parts.length === 0) return json(200, { buckets: [...buckets.keys()] });
    if (parts.length === 1) {
      const [bucket] = parts;
      if (method === 'PUT') {
        if (buckets.has(bucket)) return error(409, 'BucketNameExist');
        buckets.set(bucket, new Map());
        return route.fulfill({ status: 201 });
      }
      if (!buckets.has(bucket)) return error(404, 'BucketNotExist');
      if (method === 'GET') return json(200, { keys: [...buckets.get(bucket).keys()] });
      if (method === 'DELETE') {
        if (buckets.get(bucket).size) return error(409, 'BucketNotEmpty');
        buckets.delete(bucket);
        return route.fulfill({ status: 204 });
      }
    }
    if (parts.length === 2) {
      const [bucket, key] = parts;
      if (!buckets.has(bucket)) return error(404, 'BucketNotExist');
      const objects = buckets.get(bucket);
      if (method === 'PUT') {
        objects.set(key, request.postDataBuffer());
        return route.fulfill({ status: 204 });
      }
      if (method === 'DELETE') {
        objects.delete(key);
        return route.fulfill({ status: 204 });
      }
      if (method === 'GET') {
        if (!objects.has(key)) return error(404, 'ObjectNotExist');
        return route.fulfill({
          status: 200,
          contentType: 'application/octet-stream',
          body: objects.get(key),
        });
      }
    }
    return error(500, 'InternalError');
  });
  return { buckets, requests };
}

async function openUI(page) {
  await page.goto(`${origin}/_ui/`);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
}

async function openBucket(page, name) {
  await page.getByRole('button', { name, exact: true }).click();
}

async function openCreate(page) {
  await page.getByRole('button', { name: '创建桶' }).click();
  await expect(page.locator('#create-dialog')).toBeVisible();
}

async function openUpload(page) {
  await page.getByRole('button', { name: '上传文件' }).click();
  await expect(page.locator('#upload-dialog')).toBeVisible();
}

test('用户可创建桶、上传和下载原字节、删除对象及空桶', async ({ page }) => {
  const { buckets, requests } = await mockAPI(page);
  await page.addInitScript(() => { window.showSaveFilePicker = undefined; });
  page.on('dialog', dialog => dialog.accept());
  await openUI(page);
  expect(requests).toContainEqual({ method: 'GET', path: '/' });
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toHaveCount(0);
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('photos');
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toBeVisible();
  await openBucket(page, 'photos');
  await openUpload(page);
  const bytes = Buffer.from([0, 255, 10, 65]);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'a.bin', mimeType: 'application/octet-stream', buffer: bytes });
  await page.getByRole('button', { name: '上传', exact: true }).click();
  await expect(page.locator('#upload-dialog')).toBeHidden();
  await expect(page.getByRole('button', { name: '下载' })).toBeVisible();
  await expect(page.getByLabel('文件', { exact: true })).toHaveValue('');
  await expect(page.locator('#object-key')).toHaveValue('');
  await expect(page.locator('#selected-file')).toHaveText('未选择文件');
  expect(buckets.get('photos').get('a.bin')).toEqual(bytes);
  const downloadEvent = page.waitForEvent('download');
  await page.getByRole('button', { name: '下载' }).click();
  const download = await downloadEvent;
  expect(download.suggestedFilename()).toBe('a.bin');
  expect(await readFile(await download.path())).toEqual(bytes);
  await page.locator('#object-list li').getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect(page.getByRole('button', { name: '下载' })).toHaveCount(0);
  await page.getByRole('button', { name: '返回桶列表' }).click();
  await page.locator('#bucket-list li').filter({ hasText: 'photos' }).getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toHaveCount(0);
});

test('首页和桶内列表能显示全部条目，空桶没有对象', async ({ page }) => {
  const { requests } = await mockAPI(page, {
    photos: { 'a.txt': Buffer.from('a'), 'b.txt': Buffer.from('b') },
    documents: {},
  });
  await openUI(page);
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'documents', exact: true })).toBeVisible();
  await expect(page.locator('.list-panel .panel-heading #bucket-count')).toHaveText('2 个桶');
  await openBucket(page, 'photos');
  await expect(page.locator('#object-list li').filter({ hasText: 'a.txt' }).getByRole('button', { name: '下载' })).toBeVisible();
  await expect(page.locator('#object-list li').filter({ hasText: 'b.txt' }).getByRole('button', { name: '下载' })).toBeVisible();
  await page.getByRole('button', { name: '返回桶列表' }).click();
  await openBucket(page, 'documents');
  await expect(page.getByRole('button', { name: '下载' })).toHaveCount(0);
  expect(requests).toContainEqual({ method: 'GET', path: '/documents' });
});

test('编辑桶名时就地校验，改正后清除提示', async ({ page }) => {
  const { requests } = await mockAPI(page);
  await openUI(page);
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('a');
  for (const name of ['', 'A', 'a_b', '-a', 'a-', 'a'.repeat(64)]) {
    await page.getByRole('textbox', { name: '桶名' }).fill(name);
    if (name === '') await expect(page.locator('#bucket-name-error')).toBeHidden();
    else await expect(page.locator('#bucket-name-error')).toBeVisible();
    await page.getByRole('button', { name: '创建', exact: true }).click();
    await expect(page.locator('#bucket-name-error')).toBeVisible();
    await expect(page.getByRole('textbox', { name: '桶名' })).toHaveAttribute('aria-invalid', 'true');
    expect(requests.filter(r => r.method === 'PUT')).toHaveLength(0);
  }
  await page.getByRole('textbox', { name: '桶名' }).fill('photos');
  await expect(page.locator('#bucket-name-error')).toBeHidden();
  await expect(page.getByRole('textbox', { name: '桶名' })).toHaveAttribute('aria-invalid', 'false');
  await page.getByRole('textbox', { name: '桶名' }).fill('');
  await expect(page.locator('#bucket-name-error')).toBeHidden();
});

test('合法边界的桶名可以提交', async ({ page }) => {
  const { buckets } = await mockAPI(page);
  await openUI(page);
  for (const name of ['a', 'a'.repeat(63), 'a-9']) {
    await openCreate(page);
    await page.getByRole('textbox', { name: '桶名' }).fill(name);
    await page.getByRole('button', { name: '创建', exact: true }).click();
    await expect.poll(() => buckets.has(name)).toBe(true);
    await expect(page.locator('#create-dialog')).toBeHidden();
  }
});

test('编辑对象 key 时就地校验，改正后清除提示', async ({ page }) => {
  const { requests } = await mockAPI(page, { photos: {} });
  await openUI(page);
  await openBucket(page, 'photos');
  await openUpload(page);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'bad name.txt', mimeType: 'text/plain', buffer: Buffer.from('x') });
  await expect(page.getByRole('textbox', { name: '对象 key' })).toBeVisible();
  for (const key of ['', '.hidden', '_a', '-a', 'a/b', 'a!b', 'a'.repeat(256)]) {
    await page.getByRole('textbox', { name: '对象 key' }).fill(key);
    if (key === '') await expect(page.locator('#object-key-error')).toBeHidden();
    else await expect(page.locator('#object-key-error')).toBeVisible();
    await page.getByRole('button', { name: '上传', exact: true }).click();
    await expect(page.locator('#object-key-error')).toBeVisible();
    await expect(page.getByRole('textbox', { name: '对象 key' })).toHaveAttribute('aria-invalid', 'true');
    expect(requests.filter(r => r.method === 'PUT')).toHaveLength(0);
  }
  await page.getByRole('textbox', { name: '对象 key' }).fill('valid.txt');
  await expect(page.locator('#object-key-error')).toBeHidden();
  await expect(page.getByRole('textbox', { name: '对象 key' })).toHaveAttribute('aria-invalid', 'false');
  await page.getByRole('textbox', { name: '对象 key' }).fill('');
  await expect(page.locator('#object-key-error')).toBeHidden();
});

test('空文件配合法定 key 仍不能上传', async ({ page }) => {
  const { requests } = await mockAPI(page, { photos: {} });
  await openUI(page);
  await openBucket(page, 'photos');
  await openUpload(page);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'empty.txt', mimeType: 'text/plain', buffer: Buffer.alloc(0) });
  await page.getByRole('button', { name: '上传', exact: true }).click();
  await expect(page.locator('#file-error')).toBeVisible();
  expect(requests.filter(r => r.method === 'PUT')).toHaveLength(0);
});

test('文件名非法时可填写合法 key，合法边界可以上传', async ({ page }) => {
  const { buckets } = await mockAPI(page, { photos: {} });
  await openUI(page);
  await openBucket(page, 'photos');
  for (const key of ['a', 'a'.repeat(255), 'a.z_9-A']) {
    await openUpload(page);
    await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'bad name.txt', mimeType: 'text/plain', buffer: Buffer.from('x') });
    await page.getByRole('textbox', { name: '对象 key' }).fill(key);
    await page.getByRole('button', { name: '上传', exact: true }).click();
    await expect.poll(() => buckets.get('photos').has(key)).toBe(true);
    await expect(page.locator('#upload-dialog')).toBeHidden();
  }
});

test('覆盖对象前询问用户；取消不上传，确认后内容更新', async ({ page }) => {
  const { buckets, requests } = await mockAPI(page, { photos: { 'a.txt': Buffer.from('old') } });
  await openUI(page);
  await openBucket(page, 'photos');
  await openUpload(page);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('new') });
  let confirmOverwrite = false;
  page.on('dialog', dialog => confirmOverwrite ? dialog.accept() : dialog.dismiss());
  await page.getByRole('button', { name: '上传', exact: true }).click();
  await expect(page.locator('#upload-dialog')).toBeVisible();
  expect(requests.filter(r => r.method === 'PUT')).toHaveLength(0);
  expect(buckets.get('photos').get('a.txt').toString()).toBe('old');
  confirmOverwrite = true;
  await page.getByRole('button', { name: '上传', exact: true }).click();
  await expect.poll(() => buckets.get('photos').get('a.txt').toString()).toBe('new');
  await expect(page.locator('#upload-dialog')).toBeHidden();
});

test('删除桶和对象前确认，取消时不发送删除请求', async ({ page }) => {
  const { buckets, requests } = await mockAPI(page, { empty: {}, photos: { 'a.txt': Buffer.from('x') } });
  await openUI(page);
  await page.locator('#bucket-list li').filter({ hasText: 'empty' }).getByRole('button', { name: '删除' }).click();
  await expect(page.locator('#delete-dialog')).toContainText('empty');
  await page.locator('#delete-dialog').getByRole('button', { name: '取消' }).click();
  expect(requests.filter(r => r.method === 'DELETE')).toHaveLength(0);
  expect(buckets.has('empty')).toBe(true);
  await page.locator('#bucket-list li').filter({ hasText: 'empty' }).getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect.poll(() => buckets.has('empty')).toBe(false);

  await openBucket(page, 'photos');
  await page.locator('#object-list li').getByRole('button', { name: '删除' }).click();
  await expect(page.locator('#delete-dialog')).toContainText('a.txt');
  await page.locator('#delete-dialog').getByRole('button', { name: '取消' }).click();
  expect(requests.filter(r => r.method === 'DELETE')).toHaveLength(1);
  expect(buckets.get('photos').has('a.txt')).toBe(true);
  await page.locator('#object-list li').getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect.poll(() => buckets.get('photos').has('a.txt')).toBe(false);
});

test('创建与上传表单从列表打开，关闭后重新打开为空', async ({ page }) => {
  const { requests } = await mockAPI(page, { photos: {} });
  await openUI(page);
  await expect(page.locator('#create-dialog')).toBeHidden();
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('draft');
  await page.locator('#create-dialog').getByRole('button', { name: '取消' }).click();
  await openCreate(page);
  await expect(page.getByRole('textbox', { name: '桶名' })).toHaveValue('');
  await page.locator('#create-dialog').getByRole('button', { name: '取消' }).click();

  await openBucket(page, 'photos');
  await expect(page.locator('#upload-dialog')).toBeHidden();
  await openUpload(page);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'draft.txt', mimeType: 'text/plain', buffer: Buffer.from('x') });
  await page.locator('#upload-dialog').getByRole('button', { name: '取消' }).click();
  await openUpload(page);
  await expect(page.getByLabel('文件', { exact: true })).toHaveValue('');
  await expect(page.getByRole('textbox', { name: '对象 key' })).toHaveValue('');
  expect(requests.filter(r => r.method === 'PUT')).toHaveLength(0);
});

test('支持保存位置选择时先让用户选择，再写入下载字节', async ({ page }) => {
  await page.addInitScript(() => {
    window.showSaveFilePicker = async ({ suggestedName }) => {
      window.savedName = suggestedName;
      return { createWritable: async () => ({
        write: async blob => { window.savedBytes = Array.from(new Uint8Array(await blob.arrayBuffer())); },
        close: async () => {},
      }) };
    };
  });
  const { requests } = await mockAPI(page, { photos: { 'a.bin': Buffer.from([0, 255, 10]) } });
  await openUI(page);
  await openBucket(page, 'photos');
  await page.getByRole('button', { name: '下载' }).click();
  await expect.poll(() => page.evaluate(() => window.savedBytes)).toEqual([0, 255, 10]);
  expect(await page.evaluate(() => window.savedName)).toBe('a.bin');
  expect(requests).toContainEqual({ method: 'GET', path: '/photos/a.bin' });
});

test('取消保存位置选择时不请求对象', async ({ page }) => {
  await page.addInitScript(() => {
    window.showSaveFilePicker = async () => { throw new DOMException('cancelled', 'AbortError'); };
  });
  const { requests } = await mockAPI(page, { photos: { 'a.bin': Buffer.from('x') } });
  await openUI(page);
  await openBucket(page, 'photos');
  await page.getByRole('button', { name: '下载' }).click();
  expect(requests).not.toContainEqual({ method: 'GET', path: '/photos/a.bin' });
  await expect(page.getByRole('alert')).toBeHidden();
});

test('重复桶和非空桶错误会显示原因，不虚报成功', async ({ page }) => {
  await mockAPI(page, { photos: { 'a.txt': Buffer.from('x') } });
  await openUI(page);
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('photos');
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await page.locator('#create-dialog').getByRole('button', { name: '取消' }).click();
  await page.locator('#bucket-list li').filter({ hasText: 'photos' }).getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect(page.getByRole('alert')).toContainText(/非空|不为空|还有对象/);
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toBeVisible();
});

test('服务错误会显示提示，页面不显示失败操作已成功', async ({ page }) => {
  const { buckets } = await mockAPI(page);
  await page.route('**/broken', route => route.fulfill({
    status: 500,
    contentType: 'application/json',
    body: JSON.stringify({ error: 'InternalError' }),
  }));
  await openUI(page);
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('broken');
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  expect(buckets.has('broken')).toBe(false);
  await expect(page.getByRole('button', { name: 'broken', exact: true })).toHaveCount(0);
});

test('创建桶成功但列表刷新失败时不误报写入失败', async ({ page }) => {
  const { buckets, requests } = await mockAPI(page);
  let listReads = 0;
  await page.route(origin + '/', route => {
    if (route.request().method() === 'GET' && ++listReads === 2) {
      return route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'InternalError' }) });
    }
    return route.fallback();
  });
  await openUI(page);
  await openCreate(page);
  await page.getByRole('textbox', { name: '桶名' }).fill('photos');
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.locator('#create-dialog')).toBeHidden();
  await expect(page.locator('#error')).toContainText(/已完成.*列表.*失败/);
  expect(buckets.has('photos')).toBe(true);
  expect(requests.filter(r => r.method === 'PUT')).toHaveLength(1);
  await page.reload();
  await expect(page.getByRole('button', { name: 'photos', exact: true })).toBeVisible();
});

test('读取桶列表失败时页面显示错误', async ({ page }) => {
  await mockAPI(page);
  await page.route(origin + '/', route => route.fulfill({
    status: 500,
    contentType: 'application/json',
    body: JSON.stringify({ error: 'InternalError' }),
  }));
  await openUI(page);
  await expect(page.getByRole('alert')).toBeVisible();
});

test('读取桶内对象列表失败时页面显示错误', async ({ page }) => {
  await mockAPI(page, { photos: { 'a.txt': Buffer.from('x') } });
  await page.route('**/photos', route => route.request().method() === 'GET'
    ? route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'InternalError' }) })
    : route.fallback());
  await openUI(page);
  await openBucket(page, 'photos');
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByRole('button', { name: '下载' })).toHaveCount(0);
});

test('上传和删除对象失败时保留已知列表并提示错误', async ({ page }) => {
  const { buckets } = await mockAPI(page, { photos: { 'a.txt': Buffer.from('old') } });
  await page.route('**/photos/b.txt', route => route.fulfill({
    status: 500,
    contentType: 'application/json',
    body: JSON.stringify({ error: 'InternalError' }),
  }));
  await page.route('**/photos/a.txt', route => route.request().method() === 'DELETE'
    ? route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'InternalError' }) })
    : route.fallback());
  await openUI(page);
  await openBucket(page, 'photos');
  await openUpload(page);
  await page.getByLabel('文件', { exact: true }).setInputFiles({ name: 'b.txt', mimeType: 'text/plain', buffer: Buffer.from('new') });
  await page.getByRole('button', { name: '上传', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  expect(buckets.get('photos').has('b.txt')).toBe(false);
  await expect(page.locator('#object-list li').filter({ hasText: 'b.txt' })).toHaveCount(0);
  await page.locator('#upload-dialog').getByRole('button', { name: '取消' }).click();
  await page.locator('#object-list li').filter({ hasText: 'a.txt' }).getByRole('button', { name: '删除' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: '确认删除' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.locator('#object-list li').filter({ hasText: 'a.txt' }).getByRole('button', { name: '下载' })).toBeVisible();
});

test('下载失败时页面提示错误', async ({ page }) => {
  await page.addInitScript(() => { window.showSaveFilePicker = undefined; });
  await mockAPI(page, { photos: { 'a.txt': Buffer.from('x') } });
  await page.route('**/photos/a.txt', route => route.fulfill({
    status: 500,
    contentType: 'application/json',
    body: JSON.stringify({ error: 'InternalError' }),
  }));
  await openUI(page);
  await openBucket(page, 'photos');
  await page.getByRole('button', { name: '下载' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
});
