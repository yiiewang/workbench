// ============================================================
// common.ts - workbench 前端共用工具函数（被主界面与看板共用）
// 由原 static/js/common.js 改造为 ES module：所有顶层声明加 export。
// ============================================================

// 统一的 localStorage key
export const STORAGE_KEY_TOKEN = 'workbench_auth_token';
export const STORAGE_KEY_USER = 'workbench-current-user';
export const STORAGE_KEY_SHARE_PWD = 'workbench-share-pwd';

// ============================================================
// 认证工具
// ============================================================

/** 从 localStorage 读取 token */
export function getAuthToken() {
  return localStorage.getItem(STORAGE_KEY_TOKEN) || '';
}

/** 获取 Authorization header（无 token 时返回空对象） */
export function authHeaders() {
  const token = getAuthToken();
  return token ? { 'Authorization': 'Bearer ' + token } : {};
}

/** 带 token 的 fetch 包装 */
export function authFetch(url: string, opts: any = {}) {
  opts.headers = opts.headers || {};
  Object.assign(opts.headers, authHeaders());
  return fetch(url, opts);
}

/** 清除登录态 */
export function clearAuthState() {
  localStorage.removeItem(STORAGE_KEY_TOKEN);
  localStorage.removeItem(STORAGE_KEY_USER);
}

/** 持久化登录态 */
export function saveAuthState(token: string, user: any) {
  localStorage.setItem(STORAGE_KEY_TOKEN, token);
  if (user) localStorage.setItem(STORAGE_KEY_USER, JSON.stringify(user));
}

/** 从 localStorage 恢复 user（离线兜底） */
export function restoreUser() {
  const saved = localStorage.getItem(STORAGE_KEY_USER);
  if (!saved) return null;
  try { return JSON.parse(saved); } catch (_) { return null; }
}

// ============================================================
// 标准响应信封 {code, msg, data} 解包
// ============================================================

// 业务状态码（与后端 internal/server/codes.go 保持一致，仅声明前端需要判断的码）
export const API_CODE = {
  OK:                 0,
  UNAUTHORIZED:       40101, // 需要登录
  INVALID_TOKEN:      40103, // token 无效或过期
  PASSWORD_NOT_SET:   40301, // 用户未设密码，引导 set-password
  PASSWORD_REQUIRED:  40304, // 分享需要密码
  INVALID_SHARE_PWD:  40305, // 分享密码错误
};

/**
 * 统一 JSON API 调用：自动带 token、解包 {code,msg,data} 信封。
 * 成功返回 data（裸载荷）；失败抛 Error（附带 code/msg/status/data 字段）。
 * 仅用于返回 JSON 信封的 API 端点；原始文件流（text/blob）仍用 authFetch。
 *
 * 401 全局拦截：token 过期/无效时自动清除认证并跳转登录页，
 * 各调用方无需重复处理 401。
 */
export async function apiCall(url: string, opts: any = {}) {
  const resp = await authFetch(url, opts);
  // 401 全局拦截：跳转登录页（排除 /api/login 和 /api/me 自身，避免循环）
  if (resp.status === 401 && !url.startsWith('/api/login') && !url.startsWith('/api/me')) {
    clearAuthState();
    const current = window.location.pathname + window.location.search;
    if (!current.startsWith('/login')) {
      window.location.href = '/login?redirect=' + encodeURIComponent(current);
    }
    const err: any = new Error('Unauthorized');
    err.status = 401;
    throw err;
  }
  let body: any = {};
  try { body = await resp.json(); } catch (_) { /* 非 JSON 响应 */ }
  if (!resp.ok || body.code !== API_CODE.OK) {
    const err: any = new Error(body.msg || `HTTP ${resp.status}`);
    err.code = body.code;
    err.msg = body.msg;
    err.status = resp.status;
    err.data = body.data;
    throw err;
  }
  return body.data;
}

// ============================================================
// 通用工具函数
// ============================================================

/** HTML 转义 */
export function escapeHtml(str: any) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

/** HTML 属性转义 */
export function escapeAttr(s: any) { return String(s).replace(/"/g, '&quot;'); }

/** 复制文本到剪贴板（带 fallback） */
export function copyToClipboard(text: string) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
  } else {
    fallbackCopy(text);
  }
}

function fallbackCopy(text: string) {
  const input = document.createElement('textarea');
  input.value = text;
  input.style.position = 'fixed';
  input.style.opacity = '0';
  document.body.appendChild(input);
  input.select();
  try { (document as any).execCommand('copy'); } catch(e) {}
  document.body.removeChild(input);
}

/** 格式化文件大小 */
export function formatSize(bytes: number) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

/** Toast 提示（需要页面有 #toast 元素） */
let toastTimer: any = null;
export function showToast(msg: string) {
  const toast = document.getElementById('toast');
  if (!toast) return;
  toast.textContent = msg;
  toast.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove('show'), 2000);
}

/** base64 → Uint8Array */
export function base64ToBytes(b64: string) {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** base64 → 文本字符串 */
export function base64ToText(b64: string) {
  return new TextDecoder().decode(base64ToBytes(b64));
}

/** 判断扩展名是否为二进制文件 */
export function isBinaryExt(ext: string) {
  const binaryExts = ['zip','tar','gz','tgz','rar','7z','bz2','xz','exe','dll','so','dylib','bin','ttf','otf','woff','woff2'];
  return binaryExts.includes(ext);
}

/** 判断扩展名是否为图片 */
export function isImageExt(ext: string) {
  return ['png','jpg','jpeg','gif','svg','webp','ico'].includes(ext);
}

/** 从文件路径提取扩展名（不含点号，小写） */
export function extFromPath(path: string) {
  return ((String(path || '')).split('.').pop() || '').toLowerCase();
}

/** 从文件路径提取文件名 */
export function baseName(path: string) {
  return String(path || '').split('/').pop() || 'file';
}
