const bucketView = document.querySelector('#buckets-view');
const objectView = document.querySelector('#objects-view');
const bucketList = document.querySelector('#bucket-list');
const objectList = document.querySelector('#object-list');
const bucketInput = document.querySelector('#bucket-name');
const fileInput = document.querySelector('#file');
const keyInput = document.querySelector('#object-key');
const selectedFile = document.querySelector('#selected-file');
const bucketCount = document.querySelector('#bucket-count');
const objectCount = document.querySelector('#object-count');
const errorBox = document.querySelector('#error');
const bucketNameError = document.querySelector('#bucket-name-error');
const objectKeyError = document.querySelector('#object-key-error');
const fileError = document.querySelector('#file-error');
const createDialog = document.querySelector('#create-dialog');
const uploadDialog = document.querySelector('#upload-dialog');
const deleteDialog = document.querySelector('#delete-dialog');

let currentBucket = null;
let buckets = [];
let keys = [];
let pendingDelete = null;

const bucketNameIsValid = name => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(name);
const keyIsValid = key => /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/.test(key);

const errorMessages = {
  InvalidBucketName: '桶名不合法：使用 1–63 个小写字母、数字或连字符，首尾不能是连字符。',
  InvalidObjectKey: '对象 key 不合法：使用 1–255 个英文字母、数字、点、下划线或连字符，首字符须为字母或数字。',
  EmptyObject: '不能上传空文件。',
  BucketNameExist: '这个桶已经存在。',
  BucketNotEmpty: '桶非空，不能删除。请先删除桶内对象。',
  BucketNotExist: '桶不存在。',
  ObjectNotExist: '对象不存在。',
  InternalError: '服务暂时无法完成操作。',
};

function showError(message) {
  const box = document.querySelector('dialog[open] .dialog-error') || errorBox;
  box.textContent = message;
  box.hidden = false;
}

function clearError() {
  for (const box of [errorBox, ...document.querySelectorAll('.dialog-error')]) {
    box.textContent = '';
    box.hidden = true;
  }
}

function clearDialogError(dialog) {
  const box = dialog.querySelector('.dialog-error');
  if (!box) return;
  box.textContent = '';
  box.hidden = true;
}

function setFieldError(input, output, message) {
  output.textContent = message;
  output.hidden = !message;
  input.setAttribute('aria-invalid', message ? 'true' : 'false');
}

function validateBucketName(showEmpty = false) {
  const valid = bucketNameIsValid(bucketInput.value);
  setFieldError(bucketInput, bucketNameError, valid || (!showEmpty && bucketInput.value === '') ? '' : errorMessages.InvalidBucketName);
  return valid;
}

function validateObjectKey(showEmpty = false) {
  const valid = keyIsValid(keyInput.value);
  setFieldError(keyInput, objectKeyError, valid || (!showEmpty && keyInput.value === '') ? '' : errorMessages.InvalidObjectKey);
  return valid;
}

async function api(method, path, body) {
  const response = await fetch(path, { method, body });
  if (response.ok) return response;
  const details = await response.json().catch(() => ({}));
  throw new Error(errorMessages[details.error] || '操作失败，请重试。');
}

async function run(action, button) {
  clearError();
  if (button) button.disabled = true;
  try {
    await action();
    return true;
  } catch (error) {
    showError(error.message || '操作失败，请重试。');
    return false;
  } finally {
    if (button) button.disabled = false;
  }
}

async function writeAndRefresh(write, refresh, button, dialog) {
  const succeeded = await run(write, button);
  if (!succeeded) return false;
  if (dialog) dialog.close();
  try {
    await refresh();
  } catch {
    showError('操作已完成，但列表刷新失败。请刷新页面查看最新结果。');
  }
  return true;
}

function makeButton(label, action, className = '') {
  const button = document.createElement('button');
  button.type = 'button';
  button.textContent = label;
  button.className = className;
  button.addEventListener('click', () => action(button));
  return button;
}

function confirmDelete(type, name, write, refresh, button) {
  document.querySelector('#delete-title').textContent = `删除${type}`;
  document.querySelector('#delete-description').textContent = `确定删除${type}“${name}”吗？此操作无法撤销。`;
  pendingDelete = { write, refresh, button };
  deleteDialog.showModal();
}

function renderBuckets() {
  bucketList.replaceChildren();
  bucketCount.textContent = `${buckets.length} 个桶`;
  if (buckets.length === 0) {
    const item = document.createElement('li');
    item.className = 'empty-state';
    item.textContent = '暂无存储桶';
    bucketList.append(item);
  }
  for (const name of buckets) {
    const item = document.createElement('li');
    item.append(
      makeButton(name, () => openBucket(name), 'item-name'),
      makeButton('删除', button => confirmDelete('存储桶', name,
        () => api('DELETE', `/${encodeURIComponent(name)}`), refreshBuckets, button), 'danger'),
    );
    bucketList.append(item);
  }
}

function renderObjects() {
  objectList.replaceChildren();
  objectCount.textContent = `${keys.length} 个对象`;
  if (keys.length === 0) {
    const item = document.createElement('li');
    item.className = 'empty-state';
    item.textContent = '暂无对象';
    objectList.append(item);
  }
  for (const key of keys) {
    const item = document.createElement('li');
    item.append(
      Object.assign(document.createElement('span'), { className: 'item-name', textContent: key }),
      makeButton('下载', button => run(() => download(key), button)),
      makeButton('删除', button => confirmDelete('对象', key,
        () => api('DELETE', objectPath(key)), refreshObjects, button), 'danger'),
    );
    objectList.append(item);
  }
}

async function refreshBuckets() {
  const response = await api('GET', '/');
  buckets = (await response.json()).buckets;
  renderBuckets();
}

async function refreshObjects() {
  const response = await api('GET', `/${encodeURIComponent(currentBucket)}`);
  keys = (await response.json()).keys;
  renderObjects();
}

function objectPath(key) {
  return `/${encodeURIComponent(currentBucket)}/${encodeURIComponent(key)}`;
}

async function openBucket(name) {
  currentBucket = name;
  keys = [];
  renderObjects();
  document.querySelector('#objects-heading').textContent = name;
  bucketView.hidden = true;
  objectView.hidden = false;
  fileInput.value = '';
  keyInput.value = '';
  selectedFile.textContent = '未选择文件';
  setFieldError(fileInput, fileError, '');
  setFieldError(keyInput, objectKeyError, '');
  await run(refreshObjects);
}

async function download(key) {
  let handle;
  if (typeof window.showSaveFilePicker === 'function') {
    try {
      handle = await window.showSaveFilePicker({ suggestedName: key });
    } catch (error) {
      if (error.name === 'AbortError') return;
      throw error;
    }
  }
  const response = await api('GET', objectPath(key));
  const blob = await response.blob();
  if (handle) {
    const writable = await handle.createWritable();
    await writable.write(blob);
    await writable.close();
    return;
  }
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = key;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

bucketInput.addEventListener('input', () => validateBucketName());
keyInput.addEventListener('input', () => validateObjectKey());

document.querySelector('#create-bucket').addEventListener('click', () => createDialog.showModal());
document.querySelector('#open-upload').addEventListener('click', () => uploadDialog.showModal());

for (const dialog of [createDialog, uploadDialog, deleteDialog]) {
  dialog.querySelector('.cancel-button').addEventListener('click', () => dialog.close());
}

for (const dialog of [createDialog, uploadDialog]) {
  dialog.addEventListener('cancel', event => {
    if (dialog.querySelector('button[type="submit"]').disabled) event.preventDefault();
  });
}

createDialog.addEventListener('close', () => {
  bucketInput.value = '';
  setFieldError(bucketInput, bucketNameError, '');
  clearDialogError(createDialog);
});

uploadDialog.addEventListener('close', () => {
  fileInput.value = '';
  keyInput.value = '';
  selectedFile.textContent = '未选择文件';
  setFieldError(fileInput, fileError, '');
  setFieldError(keyInput, objectKeyError, '');
  clearDialogError(uploadDialog);
});

deleteDialog.addEventListener('close', () => { pendingDelete = null; });
document.querySelector('#confirm-delete').addEventListener('click', () => {
  const pending = pendingDelete;
  deleteDialog.close();
  if (pending) writeAndRefresh(pending.write, pending.refresh, pending.button);
});

document.querySelector('#create-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!validateBucketName(true)) return;
  const name = bucketInput.value;
  const cancel = createDialog.querySelector('.cancel-button');
  cancel.disabled = true;
  await writeAndRefresh(() => api('PUT', `/${encodeURIComponent(name)}`),
    refreshBuckets, document.querySelector('#submit-bucket'), createDialog);
  cancel.disabled = false;
});

document.querySelector('#back').addEventListener('click', () => {
  clearError();
  currentBucket = null;
  objectView.hidden = true;
  bucketView.hidden = false;
});

fileInput.addEventListener('change', () => {
  const file = fileInput.files[0];
  keyInput.value = file && keyIsValid(file.name) ? file.name : '';
  selectedFile.textContent = file ? file.name : '未选择文件';
  setFieldError(fileInput, fileError, file && file.size === 0 ? errorMessages.EmptyObject : '');
  if (file) validateObjectKey();
  else setFieldError(keyInput, objectKeyError, '');
});

document.querySelector('#upload-form').addEventListener('submit', async event => {
  event.preventDefault();
  const file = fileInput.files[0];
  const key = keyInput.value;
  if (!file || file.size === 0) {
    setFieldError(fileInput, fileError, errorMessages.EmptyObject);
    return;
  }
  if (!validateObjectKey(true)) return;
  if (keys.includes(key) && !window.confirm(`对象 ${key} 已存在，确认覆盖吗？`)) return;
  const cancel = uploadDialog.querySelector('.cancel-button');
  cancel.disabled = true;
  await writeAndRefresh(() => api('PUT', objectPath(key), file),
    refreshObjects, document.querySelector('#upload'), uploadDialog);
  cancel.disabled = false;
});

run(refreshBuckets);
