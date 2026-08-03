// ============================================================
// common.js - workbench 前端共用工具函数
// 被 index.html 和 todo.html 共用
// ============================================================

// 统一的 localStorage key
const STORAGE_KEY_TOKEN = 'workbench_auth_token';
const STORAGE_KEY_USER = 'workbench-current-user';
const STORAGE_KEY_SHARE_PWD = 'workbench-share-pwd';

// ============================================================
// 认证工具
// ============================================================

/** 从 localStorage 读取 token */
function getAuthToken() {
  return localStorage.getItem(STORAGE_KEY_TOKEN) || '';
}

/** 获取 Authorization header（无 token 时返回空对象） */
function authHeaders() {
  const token = getAuthToken();
  return token ? { 'Authorization': 'Bearer ' + token } : {};
}

/** 带 token 的 fetch 包装 */
function authFetch(url, opts = {}) {
  opts.headers = opts.headers || {};
  Object.assign(opts.headers, authHeaders());
  return fetch(url, opts);
}

/** 清除登录态 */
function clearAuthState() {
  localStorage.removeItem(STORAGE_KEY_TOKEN);
  localStorage.removeItem(STORAGE_KEY_USER);
}

/** 持久化登录态 */
function saveAuthState(token, user) {
  localStorage.setItem(STORAGE_KEY_TOKEN, token);
  if (user) localStorage.setItem(STORAGE_KEY_USER, JSON.stringify(user));
}

/** 从 localStorage 恢复 user（离线兜底） */
function restoreUser() {
  const saved = localStorage.getItem(STORAGE_KEY_USER);
  if (!saved) return null;
  try { return JSON.parse(saved); } catch (_) { return null; }
}

// ============================================================
// 通用工具函数
// ============================================================

/** HTML 转义 */
function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

/** HTML 属性转义 */
function escapeAttr(s) { return String(s).replace(/"/g, '&quot;'); }

/** 复制文本到剪贴板（带 fallback） */
function copyToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
  } else {
    fallbackCopy(text);
  }
}

function fallbackCopy(text) {
  const input = document.createElement('textarea');
  input.value = text;
  input.style.position = 'fixed';
  input.style.opacity = '0';
  document.body.appendChild(input);
  input.select();
  try { document.execCommand('copy'); } catch(e) {}
  document.body.removeChild(input);
}

/** 格式化文件大小 */
function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

/** Toast 提示（需要页面有 #toast 元素） */
let toastTimer = null;
function showToast(msg) {
  const toast = document.getElementById('toast');
  if (!toast) return;
  toast.textContent = msg;
  toast.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove('show'), 2000);
}

/** base64 → Uint8Array */
function base64ToBytes(b64) {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** base64 → 文本字符串 */
function base64ToText(b64) {
  return new TextDecoder().decode(base64ToBytes(b64));
}

/** 判断扩展名是否为二进制文件 */
function isBinaryExt(ext) {
  const binaryExts = ['zip','tar','gz','tgz','rar','7z','bz2','xz','exe','dll','so','dylib','bin','ttf','otf','woff','woff2'];
  return binaryExts.includes(ext);
}

/** 判断扩展名是否为图片 */
function isImageExt(ext) {
  return ['png','jpg','jpeg','gif','svg','webp','ico'].includes(ext);
}

/** 从文件路径提取扩展名（不含点号，小写） */
function extFromPath(path) {
  return (path.split('.').pop() || '').toLowerCase();
}

/** 从文件路径提取文件名 */
function baseName(path) {
  return path.split('/').pop() || 'file';
}
