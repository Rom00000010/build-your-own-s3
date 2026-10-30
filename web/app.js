// 错误提示
const pageError = document.querySelector('#page-error');
const dialogErrors = document.querySelectorAll('.dialog-error');

// 桶列表
const bucketsView = document.querySelector('#buckets-view');
const bucketCount = document.querySelector('#bucket-count');
const createBucketButton = document.querySelector('#create-bucket');
const bucketList = document.querySelector('#bucket-list');
const bucketEmpty = document.querySelector('#bucket-empty');
const bucketItemTemplate = document.querySelector('#bucket-item-template');

// 对象列表
const objectsView = document.querySelector('#objects-view');
const backButton = document.querySelector('#back');
const objectsHeading = document.querySelector('#objects-heading');
const objectCount = document.querySelector('#object-count');
const openUploadButton = document.querySelector('#open-upload');
const objectList = document.querySelector('#object-list');
const objectEmpty = document.querySelector('#object-empty');
const objectItemTemplate = document.querySelector('#object-item-template');

// 创建桶弹窗
const createDialog = document.querySelector('#create-dialog');
const createForm = document.querySelector('#create-form');
const bucketInput = document.querySelector('#bucket-name');
const bucketNameError = document.querySelector('#bucket-name-error');
const createCancelButton = createDialog.querySelector('.cancel-button');
const createSubmitButton = document.querySelector('#submit-bucket');

// 上传弹窗
const uploadDialog = document.querySelector('#upload-dialog');
const uploadForm = document.querySelector('#upload-form');
const fileInput = document.querySelector('#file');
const fileNameLabel = document.querySelector('#selected-file-name');
const fileError = document.querySelector('#file-error');
const uploadCancelButton = uploadDialog.querySelector('.cancel-button');
const uploadSubmitButton = document.querySelector('#upload');

// 删除确认弹窗
const deleteDialog = document.querySelector('#delete-dialog');
const deleteTitle = document.querySelector('#delete-title');
const deleteDescription = document.querySelector('#delete-description');
const deleteCancelButton = deleteDialog.querySelector('.cancel-button');
const deleteConfirmButton = document.querySelector('#confirm-delete');

// 页面状态
let currentBucket = null;
let objectKeys = [];
let pendingDelete = null;

// 输入规则与错误提示
const bucketNameIsValid = name => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(name);

const objectKeyIsValid = key => /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/.test(key);

const errorMessages = {
  InvalidBucketName: '桶名不合法：使用 1–63 个小写字母、数字或连字符，首尾不能是连字符。',
  InvalidObjectKey: '文件名不合法：请重命名后重新选择。使用 1–255 个英文字母、数字、点、下划线或连字符，首字符须为字母或数字。',
  BucketNameExist: '这个桶已经存在。',
  BucketNotEmpty: '桶非空，不能删除。请先删除桶内对象。',
  BucketNotExist: '桶不存在。',
  ObjectNotExist: '对象不存在。',
  InternalError: '服务暂时无法完成操作。',
};

// 请求与错误处理
async function requestApi(method, path, body) {
  const response = await fetch(path, { method, body });
  if (response.ok) return response;
  const details = await response.json().catch(() => ({}));
  throw new Error(errorMessages[details.error] || '操作失败，请重试。');
}

function setOperationError(message) {
  // 删除确认弹窗没有错误框；失败时显示在页面顶部
  const box = document.querySelector('dialog[open] .dialog-error') || pageError;
  box.textContent = message;
  box.hidden = false;
}

function clearOperationErrors() {
  for (const box of [pageError, ...dialogErrors]) {
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

function setInputError(input, output, message) {
  output.textContent = message;
  output.hidden = !message;
  input.setAttribute('aria-invalid', message ? 'true' : 'false');
}

async function runAction(action, button) {
  clearOperationErrors();
  if (button) button.disabled = true;
  try {
    await action();
    return true;
  } catch (error) {
    setOperationError(error.message || '操作失败，请重试。');
    return false;
  } finally {
    if (button) button.disabled = false;
  }
}

async function writeAndRefresh(writeAction, refreshAction, button, dialog) {
  const succeeded = await runAction(writeAction, button);
  if (!succeeded) return false;
  if (dialog) dialog.close();
  try {
    await refreshAction();
  } catch {
    setOperationError('操作已完成，但列表刷新失败。请刷新页面查看最新结果。');
  }
  return true;
}

// 弹窗通用行为
for (const [dialog, submitButton] of [
  [createDialog, createSubmitButton],
  [uploadDialog, uploadSubmitButton],
]) {
  dialog.addEventListener('cancel', event => {
    if (submitButton.disabled) event.preventDefault();
  });
}

// 桶列表
async function refreshBuckets() {
  const response = await requestApi('GET', '/');
  const bucketNames = (await response.json()).buckets;
  renderBuckets(bucketNames);
}

function renderBuckets(bucketNames) {
  bucketList.replaceChildren(bucketEmpty);
  bucketCount.textContent = `${bucketNames.length} 个桶`;
  bucketEmpty.hidden = bucketNames.length > 0;
  for (const name of bucketNames) {
    const item = bucketItemTemplate.content.firstElementChild.cloneNode(true);
    const openButton = item.querySelector('[data-action="open"]');
    const deleteButton = item.querySelector('[data-action="delete"]');
    openButton.textContent = name;
    openButton.addEventListener('click', () => openBucket(name));
    deleteButton.addEventListener('click', () => openDeleteDialog('存储桶', name,
      () => requestApi('DELETE', `/${encodeURIComponent(name)}`), refreshBuckets, deleteButton));
    bucketList.append(item);
  }
}

async function openBucket(name) {
  currentBucket = name;
  const succeeded = await runAction(refreshObjects);
  if (!succeeded) {
    currentBucket = null;
    return;
  }
  objectsHeading.textContent = name;
  bucketsView.hidden = true;
  objectsView.hidden = false;
}

// 对象列表
async function refreshObjects() {
  const response = await requestApi('GET', `/${encodeURIComponent(currentBucket)}`);
  objectKeys = (await response.json()).keys;
  renderObjects();
}

function renderObjects() {
  objectList.replaceChildren(objectEmpty);
  objectCount.textContent = `${objectKeys.length} 个对象`;
  objectEmpty.hidden = objectKeys.length > 0;
  for (const key of objectKeys) {
    const item = objectItemTemplate.content.firstElementChild.cloneNode(true);
    const downloadButton = item.querySelector('[data-action="download"]');
    const deleteButton = item.querySelector('[data-action="delete"]');
    item.querySelector('.item-name').textContent = key;
    downloadButton.addEventListener('click', () => download(key));
    deleteButton.addEventListener('click', () => openDeleteDialog('对象', key,
      () => requestApi('DELETE', objectPath(key)), refreshObjects, deleteButton));
    objectList.append(item);
  }
}

function objectPath(key) {
  return `/${encodeURIComponent(currentBucket)}/${encodeURIComponent(key)}`;
}

backButton.addEventListener('click', () => {
  clearOperationErrors();
  currentBucket = null;
  objectsView.hidden = true;
  bucketsView.hidden = false;
});

// 创建桶弹窗
function validateBucketName(showEmptyError = false) {
  const name = bucketInput.value;
  const valid = bucketNameIsValid(name);
  const showMessage = !valid && (showEmptyError || name !== '');
  setInputError(bucketInput, bucketNameError, showMessage ? errorMessages.InvalidBucketName : '');
  return valid;
}

createBucketButton.addEventListener('click', () => createDialog.showModal());

bucketInput.addEventListener('input', () => validateBucketName());

createCancelButton.addEventListener('click', () => createDialog.close());

createDialog.addEventListener('close', () => {
  bucketInput.value = '';
  setInputError(bucketInput, bucketNameError, '');
  clearDialogError(createDialog);
});

createForm.addEventListener('submit', async event => {
  event.preventDefault();
  if (!validateBucketName(true)) return;
  const name = bucketInput.value;
  createCancelButton.disabled = true;
  await writeAndRefresh(() => requestApi('PUT', `/${encodeURIComponent(name)}`),
    refreshBuckets, createSubmitButton, createDialog);
  createCancelButton.disabled = false;
});

// 上传弹窗
function validateFile() {
  const file = fileInput.files[0];
  if (!file) {
    setInputError(fileInput, fileError, '请选择文件。');
    return false;
  }
  if (!objectKeyIsValid(file.name)) {
    setInputError(fileInput, fileError, errorMessages.InvalidObjectKey);
    return false;
  }
  setInputError(fileInput, fileError, '');
  return true;
}

openUploadButton.addEventListener('click', () => uploadDialog.showModal());

fileInput.addEventListener('change', () => {
  const file = fileInput.files[0];
  fileNameLabel.textContent = file ? file.name : '未选择文件';
  if (file) validateFile();
  else setInputError(fileInput, fileError, '');
});

uploadCancelButton.addEventListener('click', () => uploadDialog.close());

uploadDialog.addEventListener('close', () => {
  fileInput.value = '';
  fileNameLabel.textContent = '未选择文件';
  setInputError(fileInput, fileError, '');
  clearDialogError(uploadDialog);
});

uploadForm.addEventListener('submit', async event => {
  event.preventDefault();
  if (!validateFile()) return;
  const file = fileInput.files[0];
  const key = file.name;
  if (objectKeys.includes(key) && !window.confirm(`对象 ${key} 已存在，确认覆盖吗？`)) return;
  uploadCancelButton.disabled = true;
  await writeAndRefresh(() => requestApi('PUT', objectPath(key), file),
    refreshObjects, uploadSubmitButton, uploadDialog);
  uploadCancelButton.disabled = false;
});

// 下载
// 交给浏览器下载：浏览器负责保存位置、进度和网络错误提示
function download(key) {
  const link = document.createElement('a');
  link.href = objectPath(key);
  link.download = key;
  document.body.append(link);
  link.click();
  link.remove();
}

// 删除确认弹窗
function openDeleteDialog(type, name, deleteAction, refreshAction, button) {
  deleteTitle.textContent = `删除${type}`;
  deleteDescription.textContent = `确定删除${type}“${name}”吗？此操作无法撤销。`;
  pendingDelete = { deleteAction, refreshAction, button };
  deleteDialog.showModal();
}

deleteCancelButton.addEventListener('click', () => deleteDialog.close());

deleteDialog.addEventListener('close', () => { pendingDelete = null; });

deleteConfirmButton.addEventListener('click', () => {
  const pending = pendingDelete;
  deleteDialog.close();
  if (pending) writeAndRefresh(pending.deleteAction, pending.refreshAction, pending.button);
});

// 启动
runAction(refreshBuckets);
