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
const selectAllObjects = document.querySelector('#select-all-objects');
const selectionCount = document.querySelector('#selection-count');
const downloadSelectedButton = document.querySelector('#download-selected');
const deleteSelectedButton = document.querySelector('#delete-selected');

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
const uploadFileList = document.querySelector('#upload-file-list');
const uploadFileItemTemplate = document.querySelector('#upload-file-item-template');
const uploadCancelButton = uploadDialog.querySelector('.cancel-button');
const uploadSubmitButton = document.querySelector('#upload');

// 上传列表
const uploadListButton = document.querySelector('#open-upload-list');
const uploadPanel = document.querySelector('#upload-panel');
const closeUploadListButton = document.querySelector('#close-upload-list');
const cancelUploadsButton = document.querySelector('#cancel-uploads');
const uploadSummary = document.querySelector('#upload-summary');
const uploadTaskList = document.querySelector('#upload-task-list');
const uploadTaskItemTemplate = document.querySelector('#upload-task-item-template');

// 删除确认弹窗
const deleteDialog = document.querySelector('#delete-dialog');
const deleteTitle = document.querySelector('#delete-title');
const deleteDescription = document.querySelector('#delete-description');
const deleteCancelButton = deleteDialog.querySelector('.cancel-button');
const deleteConfirmButton = document.querySelector('#confirm-delete');

// 页面状态
let currentBucket = null;
let objectKeys = [];
let objectListRequest = 0;
let bucketListRequest = 0;
const pendingRequests = new Map();
const selectedObjects = new Set();
let pendingDelete = null;
let deletingObjects = false;
let selectedUploads = [];
const uploadTasks = [];

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
async function requestApi(method, path) {
  const bucket = decodeURIComponent(path.split('/')[1]);
  pendingRequests.set(bucket, (pendingRequests.get(bucket) || 0) + 1);
  updateBucketControls();
  try {
    const response = await fetch(path, { method });
    if (response.ok) return method === 'GET' ? await response.json() : response;
    const details = await response.json().catch(() => ({}));
    throw new Error(errorMessages[details.error] || '操作失败，请重试。');
  } finally {
    const remaining = pendingRequests.get(bucket) - 1;
    if (remaining) pendingRequests.set(bucket, remaining);
    else pendingRequests.delete(bucket);
    updateBucketControls();
  }
}

function bucketIsBusy(bucket) {
  return pendingRequests.has(bucket) || (deletingObjects && currentBucket === bucket)
    || uploadTasks.some(task => task.bucket === bucket && uploadIsActive(task));
}

function updateBucketControls() {
  const blocked = pendingRequests.has('');
  createBucketButton.disabled = blocked;
  createSubmitButton.disabled = blocked || createCancelButton.disabled || bucketIsBusy(bucketInput.value);
  for (const item of bucketList.querySelectorAll('li[data-bucket]')) {
    const bucket = item.dataset.bucket;
    item.querySelector('[data-action="delete"]').disabled = blocked || bucketIsBusy(bucket);
    item.querySelector('[data-action="open"]').disabled = blocked || pendingRequests.has(bucket);
  }
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
  if (!succeeded) return;
  if (dialog) dialog.close();
  try {
    await refreshAction();
  } catch {
    setOperationError('操作已完成，但列表刷新失败。请刷新页面查看最新结果。');
  }
}

createDialog.addEventListener('cancel', event => {
  if (createCancelButton.disabled) event.preventDefault();
});

// 桶列表
async function refreshBuckets() {
  const request = ++bucketListRequest;
  const { buckets } = await requestApi('GET', '/');
  if (request !== bucketListRequest) return;
  renderBuckets(buckets);
}

function renderBuckets(bucketNames) {
  bucketList.replaceChildren(bucketEmpty);
  bucketCount.textContent = `${bucketNames.length} 个桶`;
  bucketEmpty.hidden = bucketNames.length > 0;
  for (const name of bucketNames) {
    const item = bucketItemTemplate.content.firstElementChild.cloneNode(true);
    item.dataset.bucket = name;
    const openButton = item.querySelector('[data-action="open"]');
    const deleteButton = item.querySelector('[data-action="delete"]');
    openButton.textContent = name;
    openButton.addEventListener('click', () => openBucket(name));
    deleteButton.addEventListener('click', () => openDeleteDialog('删除存储桶',
      `确定删除存储桶“${name}”吗？此操作无法撤销。`,
      () => writeAndRefresh(() => requestApi('DELETE', `/${encodeURIComponent(name)}`),
        refreshBuckets, deleteButton)));
    bucketList.append(item);
  }
  updateBucketControls();
}

async function openBucket(name) {
  selectedObjects.clear();
  currentBucket = name;
  const succeeded = await runAction(refreshObjects);
  if (currentBucket !== name) return;
  if (!succeeded) {
    currentBucket = null;
    return;
  }
  objectsHeading.textContent = name;
  renderUploadSummary();
  bucketsView.hidden = true;
  objectsView.hidden = false;
}

// 对象列表
async function refreshObjects() {
  const bucket = currentBucket;
  const request = ++objectListRequest;
  const { keys } = await requestApi('GET', `/${encodeURIComponent(bucket)}`);
  if (bucket !== currentBucket || request !== objectListRequest) return;
  objectKeys = keys;
  renderObjects();
}

function renderObjects() {
  const availableKeys = new Set(objectKeys);
  for (const key of selectedObjects) {
    if (!availableKeys.has(key)) selectedObjects.delete(key);
  }
  objectList.replaceChildren(objectEmpty);
  objectCount.textContent = `${objectKeys.length} 个对象`;
  objectEmpty.hidden = objectKeys.length > 0;
  for (const key of objectKeys) {
    const item = objectItemTemplate.content.firstElementChild.cloneNode(true);
    const checkbox = item.querySelector('input[type="checkbox"]');
    const downloadButton = item.querySelector('[data-action="download"]');
    const deleteButton = item.querySelector('[data-action="delete"]');
    item.querySelector('.item-name').textContent = key;
    checkbox.dataset.key = key;
    checkbox.setAttribute('aria-label', `选择 ${key}`);
    checkbox.addEventListener('change', () => {
      if (checkbox.checked) selectedObjects.add(key);
      else selectedObjects.delete(key);
      updateObjectSelection();
    });
    downloadButton.addEventListener('click', () => download(key));
    deleteButton.addEventListener('click', () => openObjectDeleteDialog([key]));
    objectList.append(item);
  }
  updateObjectSelection();
}

function updateObjectSelection() {
  const count = selectedObjects.size;
  selectAllObjects.checked = objectKeys.length > 0 && count === objectKeys.length;
  selectAllObjects.disabled = objectKeys.length === 0 || deletingObjects;
  selectionCount.textContent = count ? `已选择 ${count} 个对象` : '未选择对象';
  downloadSelectedButton.disabled = count === 0;
  deleteSelectedButton.disabled = count === 0 || deletingObjects;
  deleteSelectedButton.textContent = deletingObjects ? '正在删除…' : '删除所选';
  for (const checkbox of objectList.querySelectorAll('input[type="checkbox"]')) {
    checkbox.checked = selectedObjects.has(checkbox.dataset.key);
    checkbox.disabled = deletingObjects;
    checkbox.closest('li').classList.toggle('is-selected', checkbox.checked);
  }
  for (const button of objectList.querySelectorAll('[data-action="delete"]')) {
    button.disabled = deletingObjects;
  }
}

function objectPath(key) {
  return `/${encodeURIComponent(currentBucket)}/${encodeURIComponent(key)}`;
}

backButton.addEventListener('click', () => {
  showUploadList(false);
  clearOperationErrors();
  currentBucket = null;
  selectedObjects.clear();
  updateObjectSelection();
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

bucketInput.addEventListener('input', () => {
  validateBucketName();
  updateBucketControls();
});

createCancelButton.addEventListener('click', () => createDialog.close());

createDialog.addEventListener('close', () => {
  bucketInput.value = '';
  setInputError(bucketInput, bucketNameError, '');
  clearDialogError(createDialog);
  updateBucketControls();
});

createForm.addEventListener('submit', async event => {
  event.preventDefault();
  if (createSubmitButton.disabled) return;
  if (!validateBucketName(true)) return;
  const name = bucketInput.value;
  createCancelButton.disabled = true;
  await writeAndRefresh(() => requestApi('PUT', `/${encodeURIComponent(name)}`),
    refreshBuckets, createSubmitButton, createDialog);
  createCancelButton.disabled = false;
  updateBucketControls();
});

// 上传弹窗
function renderUploadFiles() {
  const names = new Set();
  const uploading = new Set(uploadTasks
    .filter(task => task.bucket === currentBucket && uploadIsActive(task))
    .map(task => task.key));
  uploadFileList.replaceChildren();
  for (const entry of selectedUploads) {
    entry.error = '';
    if (!objectKeyIsValid(entry.file.name)) entry.error = errorMessages.InvalidObjectKey;
    else if (names.has(entry.file.name)) entry.error = '同名文件只上传列表中的第一个。';
    else if (uploading.has(entry.file.name)) entry.error = '该文件正在上传，请等待上传结束。';
    else names.add(entry.file.name);

    const item = uploadFileItemTemplate.content.firstElementChild.cloneNode(true);
    item.querySelector('.item-name').textContent = entry.file.name;
    const error = item.querySelector('.field-error');
    error.textContent = entry.error;
    error.hidden = !entry.error;
    item.classList.toggle('is-invalid', Boolean(entry.error));
    const removeButton = item.querySelector('[data-action="remove"]');
    removeButton.setAttribute('aria-label', `移除 ${entry.file.name}`);
    removeButton.addEventListener('click', () => {
      selectedUploads = selectedUploads.filter(other => other !== entry);
      renderUploadFiles();
    });
    uploadFileList.append(item);
  }
  const ready = selectedUploads.filter(entry => !entry.error).length;
  fileNameLabel.textContent = selectedUploads.length ? `已选择 ${selectedUploads.length} 个文件` : '未选择文件';
  uploadSubmitButton.textContent = ready ? `上传 ${ready} 个文件` : '上传';
  setInputError(fileInput, fileError, '');
}

openUploadButton.addEventListener('click', () => uploadDialog.showModal());

fileInput.addEventListener('change', () => {
  for (const file of fileInput.files) selectedUploads.push({ file });
  fileInput.value = '';
  renderUploadFiles();
});

uploadCancelButton.addEventListener('click', () => uploadDialog.close());

uploadDialog.addEventListener('close', () => {
  selectedUploads = [];
  renderUploadFiles();
  clearDialogError(uploadDialog);
});

uploadForm.addEventListener('submit', event => {
  event.preventDefault();
  const files = selectedUploads.filter(entry => !entry.error).map(entry => entry.file);
  if (files.length === 0) {
    setInputError(fileInput, fileError, selectedUploads.length ? '没有可上传的文件，请重新选择。' : '请选择文件。');
    return;
  }
  clearOperationErrors();
  uploadDialog.close();
  for (const file of files) startUpload(file);
  showUploadList(true);
});

// 上传列表
function uploadIsActive(task) {
  return task.state === 'uploading' || task.state === 'saving';
}

function showUploadList(open) {
  uploadPanel.hidden = !open;
  uploadListButton.setAttribute('aria-expanded', String(open));
}

function renderUploadSummary() {
  const tasks = uploadTasks.filter(task => task.bucket === currentBucket);
  for (const task of uploadTasks) task.element.hidden = task.bucket !== currentBucket;
  const active = tasks.filter(uploadIsActive).length;
  const succeeded = tasks.filter(task => task.state === 'success').length;
  const failed = tasks.filter(task => task.state === 'failed').length;
  const cancelled = tasks.filter(task => task.state === 'cancelled').length;
  uploadSummary.textContent = tasks.length
    ? `${active} 个进行中 · ${succeeded} 个成功 · ${failed} 个失败 · ${cancelled} 个取消` : '暂无上传记录';
  cancelUploadsButton.disabled = !tasks.some(task => task.state === 'uploading');
  uploadListButton.textContent = tasks.length ? `上传列表（${tasks.length}）` : '上传列表';
  updateBucketControls();
}

function renderUploadProgress(task) {
  task.element.querySelector('progress').value = task.percent;
  task.element.querySelector('.task-percent').textContent = `${task.percent}%`;
}

function renderUploadTask(task) {
  task.element.dataset.state = task.state;
  task.element.querySelector('.task-state').textContent = {
    uploading: '上传中', saving: '正在保存', success: '上传成功', failed: '上传失败', cancelled: '已取消',
  }[task.state];
  const action = task.element.querySelector('[data-action="task"]');
  action.textContent = uploadIsActive(task) ? '取消' : '移除';
  action.disabled = task.state === 'saving';
  action.setAttribute('aria-label', `${action.textContent} ${task.key}`);
  const error = task.element.querySelector('.field-error');
  error.textContent = task.error;
  error.hidden = !task.error;
  renderUploadProgress(task);
  renderUploadSummary();
  if (uploadDialog.open && task.bucket === currentBucket) renderUploadFiles();
}

function startUpload(file) {
  const element = uploadTaskItemTemplate.content.firstElementChild.cloneNode(true);
  const request = new XMLHttpRequest();
  const task = { bucket: currentBucket, key: file.name, state: 'uploading', percent: 0, error: '', element, request };
  element.querySelector('.item-name').textContent = file.name;
  element.querySelector('progress').setAttribute('aria-label', `${file.name} 上传进度`);
  element.querySelector('[data-action="task"]').addEventListener('click', () => {
    if (uploadIsActive(task)) cancelUpload(task);
    else {
      uploadTasks.splice(uploadTasks.indexOf(task), 1);
      element.remove();
      renderUploadSummary();
    }
  });
  uploadTasks.push(task);
  uploadTaskList.append(element);
  renderUploadTask(task);

  request.upload.onprogress = event => {
    if (!event.lengthComputable || event.total === 0) return;
    task.percent = Math.min(100, Math.floor(event.loaded / event.total * 100));
    renderUploadProgress(task);
  };
  request.upload.onload = () => {
    task.percent = 100;
    task.state = 'saving';
    renderUploadTask(task);
  };
  request.onloadend = async () => {
    if (task.state === 'cancelled') return;
    if (request.status >= 200 && request.status < 300) {
      task.percent = 100;
      task.state = 'saving';
      renderUploadTask(task);
      if (task.bucket === currentBucket) {
        await refreshObjects().catch(() => {
          if (task.bucket === currentBucket) setOperationError('上传已完成，但列表刷新失败。请刷新页面查看最新结果。');
        });
      }
      task.state = 'success';
    } else {
      task.state = 'failed';
      task.error = request.status === 0 ? '连接中断，请检查网络后重新上传。'
        : errorMessages[request.response?.error] || '上传失败，请重新选择文件上传。';
    }
    renderUploadTask(task);
  };
  request.open('PUT', objectPath(file.name));
  request.responseType = 'json';
  request.send(file);
}

function cancelUpload(task) {
  if (task.state !== 'uploading') return;
  task.state = 'cancelled';
  task.request.abort();
  renderUploadTask(task);
}

cancelUploadsButton.addEventListener('click', () => {
  for (const task of uploadTasks) {
    if (task.bucket === currentBucket) cancelUpload(task);
  }
});

uploadListButton.addEventListener('click', () => showUploadList(uploadPanel.hidden));
closeUploadListButton.addEventListener('click', () => {
  showUploadList(false);
  uploadListButton.focus();
});
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && !document.querySelector('dialog[open]') && !uploadPanel.hidden) {
    showUploadList(false);
    uploadListButton.focus();
  }
});

// 下载
selectAllObjects.addEventListener('change', () => {
  selectedObjects.clear();
  if (selectAllObjects.checked) {
    for (const key of objectKeys) selectedObjects.add(key);
  }
  updateObjectSelection();
});

downloadSelectedButton.addEventListener('click', () => {
  for (const key of selectedObjects) download(key);
});

// 交给浏览器下载：浏览器负责保存位置、进度和网络错误提示
function download(key) {
  const link = document.createElement('a');
  link.href = objectPath(key);
  link.download = key;
  document.body.append(link);
  link.click();
  link.remove();
}

// 对象删除
function openObjectDeleteDialog(keys) {
  if (deletingObjects || keys.length === 0) return;
  const title = keys.length > 1 ? '批量删除对象' : '删除对象';
  const description = keys.length > 1
    ? `确定删除选中的 ${keys.length} 个对象吗？此操作无法撤销。`
    : `确定删除对象“${keys[0]}”吗？此操作无法撤销。`;
  openDeleteDialog(title, description, () => deleteObjects(keys));
}

async function deleteObjects(keys) {
  if (deletingObjects) return;
  deletingObjects = true;
  backButton.disabled = true;
  openUploadButton.disabled = true;
  updateObjectSelection();
  await runAction(async () => {
    const results = await Promise.allSettled(keys.map(key => requestApi('DELETE', objectPath(key))));
    await refreshObjects();
    if (results.some(result => result.status === 'rejected')) {
      throw new Error('有对象删除失败，请重试。');
    }
  });
  deletingObjects = false;
  backButton.disabled = false;
  openUploadButton.disabled = false;
  updateObjectSelection();
  updateBucketControls();
}

deleteSelectedButton.addEventListener('click', () => openObjectDeleteDialog([...selectedObjects]));

// 删除确认弹窗
function openDeleteDialog(title, description, deleteAction) {
  deleteTitle.textContent = title;
  deleteDescription.textContent = description;
  pendingDelete = deleteAction;
  deleteDialog.showModal();
}

deleteCancelButton.addEventListener('click', () => deleteDialog.close());

deleteDialog.addEventListener('close', () => { pendingDelete = null; });

deleteConfirmButton.addEventListener('click', () => {
  const pending = pendingDelete;
  deleteDialog.close();
  if (pending) pending();
});

// 启动
runAction(refreshBuckets);
