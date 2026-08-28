// @ts-nocheck — 原生 JS 迁移代码，待 SFC 化时逐组件类型化（第3步）
import { ref, reactive, computed, watch, onMounted, nextTick } from 'vue'
import {
    tasks, showModal, isCreating, editingTask, editingDateRange,
    currentUser, authToken,
    viewingMember, orgMembers, syncStatus, lastSyncError,
    showConflictModal, conflictInfo, conflictTaskList, showManualResolve,
    conflictChoices, showConfirmSummary, confirmSummary, filterSeverity,
    orgTasks, activeTaskId,
} from '../stores/todoStore'
import { checkAuth } from '../stores/indexStore'
import * as taskApi from '../api/tasks'
import { ElNotification } from 'element-plus'
import { marked, Renderer } from 'marked'
import SparkMD5 from 'spark-md5'
import hljs from 'highlight.js/lib/core'
import 'highlight.js/styles/github.css'
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import json from 'highlight.js/lib/languages/json'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import markdown from 'highlight.js/lib/languages/markdown'
import xml from 'highlight.js/lib/languages/xml'
import cssLang from 'highlight.js/lib/languages/css'
import yaml from 'highlight.js/lib/languages/yaml'
import sql from 'highlight.js/lib/languages/sql'
import shell from 'highlight.js/lib/languages/shell'

// 注册常用语言（highlightAuto 仅在已注册语言中检测，按需注册大幅缩小体积）
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('python', python)
hljs.registerLanguage('go', go)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', cssLang)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('shell', shell)

// API 调用统一走 ../api/ 模块（tasks/files/share/auth），不再在此重复定义 apiCall。
// 原有的 API_CODE 常量也移至 api 模块内部。

export function setupTodoApp() {
    // --- Constants ---
    const STORAGE_KEY_USER = 'workbench-current-user';
    const columns = [
        { key: 'todo', label: '待办', icon: '🔴' },
        { key: 'progress', label: '进行中', icon: '🟡' },
        { key: 'done', label: '已完成', icon: '✅' },
    ];
    const priorityLabelMap = { high: '高', medium: '中', low: '低' };
    const statusIconMap = { todo: '🔴', progress: '🟡', done: '✅', conflict: '⚠️' };

    // --- Reactive State ---
    // tasks, auth/user, modal, viewingMember, syncStatus → imported from todoStore
    const activeTab = ref('quadrant');
    const draggingTaskId = ref(null);
    // 列表视图
    const listNewTitle = ref('');
    const listViewMode = ref('status'); // 'status' | 'quadrant'
    const collapsedGroups = reactive({
        todo: false, progress: false, done: false,
        q_q1: false, q_q2: false, q_q3: false, q_q4: false,
    });
    const calendarDragOverDate = ref(null);
    // computed for markdown-editor v-model (get/set editingTask.content)
    const editingTaskContent = computed({
        get: () => editingTask.value.content || '',
        set: (val) => { editingTask.value = { ...editingTask.value, content: val }; }
    });
    const newTaskTitle = ref({ todo: '', progress: '', done: '', conflict: '' });
    const newTaskForDate = ref({});
    const currentWeekStart = ref(getWeekStart(new Date()));

    // ========== Version Control Utilities ==========
    const STORAGE_KEY_DEVICE = 'todo-device-id';

    // 获取/生成设备唯一 ID（持久化到 localStorage）
    function getDeviceId() {
        let id = localStorage.getItem(STORAGE_KEY_DEVICE);
        if (!id) {
            id = 'dev-' + Date.now() + '-' + Math.random().toString(36).slice(2, 10);
            localStorage.setItem(STORAGE_KEY_DEVICE, id);
        }
        return id;
    }

    // 计算任务数据的 MD5 哈希值（与服务端 internal/db/tasks.go md5Hex 算法保持一致）。
    // 必须用真正的 MD5（128 位、32 hex 字符），否则与服务端 crypto/md5 永远不匹配，
    // 导致 checkConflict 误判为冲突。
    async function calcTasksMd5(tasks) {
        const text = JSON.stringify(tasks);
        return SparkMD5.hash(text);
    }

    // 构建版本信息对象
    async function buildVersion(tasks, baseVersion = null) {
        const md5 = await calcTasksMd5(tasks);
        return {
            md5,
            timestamp: Date.now(),
            deviceId: getDeviceId(),
            // 保留原始 baseMd5：本地编辑时不更新 baseMd5，仅同步时更新
            // baseVersion.baseMd5 存在时沿用（跨多次本地编辑保持不变）
            baseMd5: baseVersion ? (baseVersion.baseMd5 || baseVersion.md5) : md5,
            baseTimestamp: baseVersion ? (baseVersion.baseTimestamp || baseVersion.timestamp) : Date.now(),
        };
    }

    // ========== Conflict Detection & Resolution ==========
    // 冲突检测：比较本地版本和服务器版本
    function checkConflict(localVersion, serverVersion) {
        // 双方都无版本信息
        if (!localVersion && !serverVersion) {
            return { hasConflict: false, action: 'none' };
        }
        // 只有服务器有数据
        if (!localVersion) {
            return { hasConflict: false, action: 'download' };
        }
        // 只有本地有数据
        if (!serverVersion) {
            return { hasConflict: false, action: 'upload' };
        }
        // 数据完全一致
        if (localVersion.md5 === serverVersion.md5) {
            return { hasConflict: false, action: 'none' };
        }

        // 迁移：baseMd5 为旧占位符 "init"，无法正确判断冲突，强制下载
        if (localVersion.baseMd5 === 'init') {
            return { hasConflict: false, action: 'download' };
        }

        // 判断本地是否有修改（当前 md5 != 基准 md5）
        const localModified = localVersion.md5 !== localVersion.baseMd5;
        // 判断服务器是否有修改（服务器当前 md5 != 本地基准 md5）
        const serverModified = serverVersion.md5 !== localVersion.baseMd5;

        if (localModified && serverModified) {
            // 双方都有修改，冲突！
            return { hasConflict: true, action: 'merge' };
        }
        if (localModified && !serverModified) {
            return { hasConflict: false, action: 'upload' };
        }
        if (!localModified && serverModified) {
            return { hasConflict: false, action: 'download' };
        }
        if (!localModified && !serverModified) {
            // 双方都没修改（但 md5 不一致，可能数据损坏）
            return { hasConflict: false, action: 'none' };
        }
    }

    // 包装任务数据：附加版本信息
    async function wrapData(tasks, baseVersion = null) {
        const version = await buildVersion(tasks, baseVersion);
        return { version, tasks };
    }

    // 解析存储数据：新格式 {version, tasks}
    function unwrapData(data) {
        if (!data) return { version: null, tasks: [] };
        if (data.version && Array.isArray(data.tasks)) {
            const v = data.version;
            // 迁移：旧版本用 simpleHash（1-8 hex 字符），与新 SparkMD5（32 hex 字符）不兼容。
            // 丢弃 version（保留 tasks）以强制从服务器重新同步，避免首次升级时误报冲突。
            if (typeof v.md5 === 'string' && v.md5.length < 32) {
                return { version: null, tasks: data.tasks };
            }
            return { version: v, tasks: data.tasks };
        }
        // 无法识别的格式
        return { version: null, tasks: [] };
    }

    // 归一化任务列表的 assignee：历史脏数据可能残留 number（主键改造中间态），
    // 统一转字符串化 id。否则前端 JSON.stringify(number) 与后端 json.Marshal(string)
    // 的 md5 永远不一致，导致持续误报冲突。
    function normalizeAssignees(list) {
        return (list || []).map(t => t.assignee == null ? t : { ...t, assignee: String(t.assignee) });
    }

    // ========== /tasks.json 请求缓存层 ==========
    // 多个函数都需要拉取 /tasks.json，缓存 3 秒避免重复请求
    let _tasksJsonCache = null;
    let _tasksJsonCacheTime = 0;
    const TASKS_JSON_CACHE_TTL = 3000;

    async function fetchTasksJSON(forceRefresh = false) {
        const now = Date.now();
        if (!forceRefresh && _tasksJsonCache && (now - _tasksJsonCacheTime) < TASKS_JSON_CACHE_TTL) {
            return _tasksJsonCache;
        }
        try {
            const data = await taskApi.getTasks();
            _tasksJsonCache = data;
            _tasksJsonCacheTime = now;
            return data;
        } catch (e) {
            return null;
        }
    }

    function invalidateTasksJsonCache() {
        _tasksJsonCache = null;
        _tasksJsonCacheTime = 0;
    }

    // --- Auto Status Update ---
    // 只计算延期天数，不自动修改状态（状态由用户手动管理）
    function autoUpdateStatus() {
        const today = formatDate(new Date());
        const todayDt = new Date(today + 'T00:00:00');
        for (const task of tasks.value) {
            if (task.status === 'done' || task.status === 'conflict') continue;
            if (!task.due) continue;
            // 计算实际延期天数：today - due（只有 today > due 才 > 0）
            const dueDt = new Date(task.due + 'T00:00:00');
            if (dueDt < todayDt && task.progress < 100) {
                const diffDays = Math.round((todayDt - dueDt) / 86400000);
                task.postponedCount = diffDays > 0 ? diffDays : 0;
                if (!task.postponedFrom) {
                    task.postponedFrom = task.scheduled;
                }
            }
        }
    }

    // --- Load tasks for current user ---
    // conflict state → imported from todoStore

    async function loadUserTasks() {
        if (!currentUser.value) return;
        const { userId, orgId } = currentUser.value;
        const userKey = STORAGE_KEY_USER + '_' + userId;

        // 1. 从 localStorage 加载（解析新格式，自动迁移旧格式）
        let localVersion = null;
        let localTasks = [];
        const saved = localStorage.getItem(userKey);
        if (saved) {
            try {
                const parsed = JSON.parse(saved);
                const unwrapped = unwrapData(parsed);
                localVersion = unwrapped.version;
                localTasks = normalizeAssignees(unwrapped.tasks);
            } catch (e) {
                localTasks = [];
            }
        }

        // 2. 从服务器同步（如果可用）
        let serverVersion = null;
        let serverTasks = null;
        try {
            const data = await fetchTasksJSON();
            if (data) {
                const org = (data.orgs || {})[orgId];
                if (org && org[userId]) {
                    const unwrapped = unwrapData(org[userId]);
                    serverVersion = unwrapped.version;
                    serverTasks = normalizeAssignees(unwrapped.tasks);
                }
            }
        } catch (e) { /* 离线模式，使用本地缓存 */ }

        // 3. 冲突检测
        const conflict = checkConflict(localVersion, serverVersion);

        if (conflict.hasConflict) {
            // 有冲突，弹出冲突处理 UI
            conflictInfo.value = {
                local: { version: localVersion, tasks: localTasks },
                server: { version: serverVersion, tasks: serverTasks },
                resolution: null,
            };
            showConflictModal.value = true;
            // 暂时使用本地数据
            tasks.value = localTasks;
        } else {
            // 无冲突，自动同步
            // 优先使用服务器数据（如果服务器有数据）
            if (serverTasks && serverTasks.length > 0) {
                tasks.value = serverTasks;
                // 更新 localStorage（包含版本信息）
                // 直接使用服务器 version，不重算哈希，确保 md5 与服务器一致
                // 修正 baseMd5 = md5，标记客户端与服务器已同步
                const syncedVersion = { ...serverVersion, baseMd5: serverVersion.md5, baseTimestamp: serverVersion.timestamp };
                const wrapped = { version: syncedVersion, tasks: serverTasks };
                localStorage.setItem(userKey, JSON.stringify(wrapped));
            } else if (localTasks && localTasks.length > 0) {
                // 服务器无数据，使用本地数据
                tasks.value = localTasks;
                // 异步上传到服务器
                syncToServer(userId, orgId);
            }
            // 双方都无数据，保持 tasks.value 为空
        }

        // 4. 加载组织成员列表
        await loadOrgMembers();

        if (tasks.value.length > 0) {
            autoUpdateStatus();
        }
    }

    // --- Conflict Resolution ---
    async function resolveConflict(action) {
        if (!conflictInfo.value) return;
        const { local, server } = conflictInfo.value;
        const { userId, orgId } = currentUser.value;
        const userKey = STORAGE_KEY_USER + '_' + userId;

        if (action === 'local') {
            // 保留本地版本
            tasks.value = local.tasks;
            const wrapped = await wrapData(local.tasks, server ? server.version : null);
            localStorage.setItem(userKey, JSON.stringify(wrapped));
            syncToServer(userId, orgId);
        } else if (action === 'server') {
            // 保留服务器版本
            tasks.value = server.tasks;
            const wrapped = await wrapData(server.tasks, server.version);
            localStorage.setItem(userKey, JSON.stringify(wrapped));
        } else if (action === 'merge') {
            // 智能合并：基于任务级 lastModified 时间戳（暂未实现，默认保留本地）
            showToast('智能合并功能开发中，暂使用本地版本', 'warning');
            tasks.value = local.tasks;
            const wrapped = await wrapData(local.tasks, server ? server.version : null);
            localStorage.setItem(userKey, JSON.stringify(wrapped));
            syncToServer(userId, orgId);
        }

        showConflictModal.value = false;
        conflictInfo.value = null;
        showToast('冲突已解决', 'success');
    }

    // --- Manual Conflict Resolution ---
    // --- 任务比对工具 ---
    const DIFF_FIELDS = ['title', 'status', 'priority', 'progress', 'due', 'scheduled', 'content', 'assignee', 'postponedCount', 'confirmedDate'];
    const FIELD_LABELS = {
        title: '标题', status: '状态', priority: '优先级', progress: '进度',
        due: '截止', scheduled: '计划', content: '内容', assignee: '负责人',
        postponedCount: '延期次数', confirmedDate: '确认日期'
    };
    const HIGH_SEVERITY_FIELDS = new Set(['status', 'priority']);
    const MEDIUM_SEVERITY_FIELDS = new Set(['due', 'scheduled', 'confirmedDate']);

    // 字段中文名
    function fieldLabel(f) { return FIELD_LABELS[f] || f; }

    // 格式化字段值用于展示
    function formatField(task, f) {
        if (!task) return '—';
        if (f === 'status') return getStatusLabel(task[f]);
        if (f === 'priority') return priorityLabel(task[f]);
        if (f === 'progress') return (task[f] || 0) + '%';
        return task[f] ?? '—';
    }

    // 差异字段的严重度（用于 tag 颜色）
    function diffSeverity(f) {
        if (HIGH_SEVERITY_FIELDS.has(f)) return 'high';
        if (MEDIUM_SEVERITY_FIELDS.has(f)) return 'medium';
        return 'low';
    }

    // 计算任务冲突严重度
    function getSeverity(diffs) {
        if (diffs.some(f => HIGH_SEVERITY_FIELDS.has(f))) return 'high';
        if (diffs.some(f => MEDIUM_SEVERITY_FIELDS.has(f))) return 'medium';
        return 'low';
    }

    // 冲突类型中文标签
    function typeLabel(row) {
        if (row.type === 'local-only') return '仅本地';
        if (row.type === 'server-only') return '仅服务器';
        return '两端均有';
    }

    // 展示字段列表：both 类型只显示差异字段 + 核心字段
    function showFields(row) {
        if (row.type === 'both' && row.diffs.length) {
            const seen = new Set(row.diffs);
            for (const f of ['title', 'status', 'priority']) {
                if (!seen.has(f)) seen.add(f);
            }
            return [...seen].slice(0, 6);
        }
        return ['title', 'status', 'priority', 'due', 'scheduled', 'progress'].slice(0, 6);
    }

    // diff 字段分类：changed / none
    function diffClass(row, f) {
        return row.diffs.includes(f) ? 'changed' : 'none';
    }

    // 按严重度筛选后的列表
    const filteredConflictList = computed(() => {
        if (filterSeverity.value === 'all') return conflictTaskList.value;
        return conflictTaskList.value.filter(t => t.severity === filterSeverity.value);
    });

    // 计算确认汇总
    function buildConfirmSummary() {
        let localCount = 0, serverCount = 0, fieldCount = 0, dropCount = 0;
        for (const item of conflictTaskList.value) {
            const c = conflictChoices[item.id];
            if (!c) { dropCount++; continue; }
            if (c._mode === 'drop') { dropCount++; }
            else if (c._mode === 'local') { localCount++; }
            else if (c._mode === 'server') { serverCount++; }
            else if (c._mode === 'field') { fieldCount++; }
        }
        confirmSummary.localCount = localCount;
        confirmSummary.serverCount = serverCount;
        confirmSummary.fieldCount = fieldCount;
        confirmSummary.dropCount = dropCount;
    }

    // 计算单个任务的 MD5（同步，使用 SparkMD5）
    function taskMD5(task) {
        const obj = {};
        for (const f of DIFF_FIELDS) {
            obj[f] = task[f] === undefined ? null : task[f];
        }
        return SparkMD5.hash(JSON.stringify(obj));
    }

    // 返回差异字段列表（用于高亮）
    function getDiffFields(lt, st) {
        const diffs = [];
        for (const f of DIFF_FIELDS) {
            const va = lt[f] === undefined ? null : lt[f];
            const vb = st[f] === undefined ? null : st[f];
            if (va !== vb) diffs.push(f);
        }
        return diffs;
    }

    function openManualResolve() {
        if (!conflictInfo.value) return;
        const { local, server } = conflictInfo.value;
        const localMap = {};
        (local.tasks || []).forEach(t => { localMap[t.id] = t; });
        const serverMap = {};
        (server.tasks || []).forEach(t => { serverMap[t.id] = t; });

        const list = [];
        const allIds = new Set([
            ...(local.tasks || []).map(t => t.id),
            ...(server.tasks || []).map(t => t.id)
        ]);

        for (const id of allIds) {
            const lt = localMap[id];
            const st = serverMap[id];

            // 只在本地存在
            if (lt && !st) {
                list.push({ id, title: lt.title, local: { ...lt }, server: null, diffs: [], type: 'local-only', severity: 'low' });
                conflictChoices[id] = { _mode: 'local' };
                continue;
            }
            // 只在服务器存在
            if (!lt && st) {
                list.push({ id, title: st.title, local: null, server: { ...st }, diffs: [], type: 'server-only', severity: 'low' });
                conflictChoices[id] = { _mode: 'server' };
                continue;
            }
            // 双方都存在：用 MD5 快速判断
            if (lt && st) {
                const md5Local = taskMD5(lt);
                const md5Server = taskMD5(st);
                if (md5Local !== md5Server) {
                    const diffs = getDiffFields(lt, st);
                    const severity = getSeverity(diffs);
                    list.push({ id, title: lt.title || st.title, local: { ...lt }, server: { ...st }, diffs, type: 'both', severity });
                    const localNewer = (local.version?.timestamp || 0) >= (server.version?.timestamp || 0);
                    // 初始化 conflictChoices：默认整条取较新的一端，同时初始化各字段选择
                    const choice = { _mode: localNewer ? 'local' : 'server' };
                    for (const f of diffs) { choice[f] = localNewer ? 'local' : 'server'; }
                    conflictChoices[id] = choice;
                }
            }
        }

        // fallback：若逐条比对无差异，但整体 md5 不一致（可能差异仅在非 DIFF_FIELDS 字段），
        // 按 id 差异推入列表，标记为 metadata-only 差异
        if (list.length === 0) {
            const localIds = new Set((local.tasks || []).map(t => t.id));
            const serverIds = new Set((server.tasks || []).map(t => t.id));
            // 只在本地存在的任务
            for (const id of localIds) {
                if (!serverIds.has(id)) {
                    const lt = localMap[id];
                    list.push({ id, title: lt?.title || '(无标题)', local: { ...lt }, server: null, diffs: [], type: 'local-only', severity: 'low' });
                    conflictChoices[id] = { _mode: 'local' };
                }
            }
            // 只在服务器存在的任务
            for (const id of serverIds) {
                if (!localIds.has(id)) {
                    const st = serverMap[id];
                    list.push({ id, title: st?.title || '(无标题)', local: null, server: { ...st }, diffs: [], type: 'server-only', severity: 'low' });
                    conflictChoices[id] = { _mode: 'server' };
                }
            }
            // 双方都存在但逐条 MD5 一致 → 仅在非比对字段有差异（如 id 本身、autoPostponed 等）
            for (const id of localIds) {
                if (serverIds.has(id)) {
                    const lt = localMap[id], st = serverMap[id];
                    if (!list.find(t => t.id === id)) {
                        list.push({ id, title: lt?.title || st?.title || '(无标题)', local: { ...lt }, server: { ...st }, diffs: [], type: 'both', severity: 'low' });
                        const localNewer = (local.version?.timestamp || 0) >= (server.version?.timestamp || 0);
                        conflictChoices[id] = { _mode: localNewer ? 'local' : 'server' };
                    }
                }
            }
        }

        if (list.length === 0) {
            // 冲突检测通过但逐任务比对无差异，无需处理
        }

        conflictTaskList.value = list;
        filterSeverity.value = 'all';
        showManualResolve.value = true;
    }

    function selectAllLocal() {
        for (const item of conflictTaskList.value) {
            if (item.type === 'server-only') {
                // 无本地版本，全选本地 = 丢弃服务器独有任务
                conflictChoices[item.id] = { _mode: 'drop' };
            } else {
                conflictChoices[item.id] = { _mode: 'local' };
                if (item.type === 'both') {
                    for (const f of item.diffs) { conflictChoices[item.id][f] = 'local'; }
                }
            }
        }
    }

    function selectAllServer() {
        for (const item of conflictTaskList.value) {
            if (item.type === 'local-only') {
                // 无服务器版本，全选服务器 = 丢弃本地独有任务
                conflictChoices[item.id] = { _mode: 'drop' };
            } else {
                conflictChoices[item.id] = { _mode: 'server' };
                if (item.type === 'both') {
                    for (const f of item.diffs) { conflictChoices[item.id][f] = 'server'; }
                }
            }
        }
    }

    // 点「确认合并」：先弹汇总
    function confirmManualResolve() {
        if (!conflictInfo.value) return;
        buildConfirmSummary();
        showConfirmSummary.value = true;
    }

    // 汇总弹窗点「确认执行合并」
    async function executeMerge() {
        if (!conflictInfo.value) return;
        const { local, server } = conflictInfo.value;
        const { userId, orgId } = currentUser.value;
        const userKey = STORAGE_KEY_USER + '_' + userId;

        const localMap = {};
        (local.tasks || []).forEach(t => { localMap[t.id] = t; });
        const serverMap = {};
        (server.tasks || []).forEach(t => { serverMap[t.id] = t; });

        // 构建合并结果：以本地完整列表为基底，按用户选择覆盖冲突项
        const mergedMap = {};
        for (const t of (local.tasks || [])) { mergedMap[t.id] = { ...t }; }

        // 服务器独有的任务（本地不存在）默认保留
        for (const t of (server.tasks || [])) {
            if (!mergedMap[t.id]) mergedMap[t.id] = { ...t };
        }

        // 对冲突项按用户选择覆盖
        for (const item of conflictTaskList.value) {
            const c = conflictChoices[item.id];
            if (!c) continue;

            if (c._mode === 'drop') {
                delete mergedMap[item.id];
            } else if (c._mode === 'local') {
                mergedMap[item.id] = { ...item.local };
            } else if (c._mode === 'server') {
                mergedMap[item.id] = { ...item.server };
            } else if (c._mode === 'field') {
                const base = { ...item.local };
                for (const f of item.diffs) {
                    if (c[f] === 'server' && item.server) {
                        base[f] = item.server[f];
                    }
                }
                mergedMap[item.id] = base;
            }
        }

        const merged = Object.values(mergedMap);

        tasks.value = merged;
        const wrapped = await wrapData(merged, server ? server.version : null);
        localStorage.setItem(userKey, JSON.stringify(wrapped));

        // 直接上传，绕过 syncToServer 的二次冲突检测。
        // executeMerge 已是用户决策的最终结果（手动合并完成），无需再做冲突检测；
        // syncToServer 内部会再次 fetch 服务端并 checkConflict，
        // 但此时服务端可能因其他客户端写入导致 serverVersion 与 wrapped.version.baseMd5 不匹配，
        // 触发误报冲突 → return → 不发 PUT。改为直接 PUT 与 localStorage 同步。
        try {
            const all = (await fetchTasksJSON()) || { orgs: {}, lastUpdated: '' };
            if (!all.orgs) all.orgs = {};
            if (!all.orgs[orgId]) all.orgs[orgId] = {};
            all.orgs[orgId][userId] = wrapped;
            all.lastUpdated = new Date().toISOString();
            await taskApi.putTasks(all);
            invalidateTasksJsonCache();
            localStorage.setItem(userKey, JSON.stringify({ version: wrapped.version, tasks: tasks.value }));
            syncStatus.value = 'idle';
        } catch (err) {
            syncStatus.value = 'error';
            lastSyncError.value = err.message || '未知错误';
            showToast('合并结果上传失败', 'error');
        }

        showConfirmSummary.value = false;
        showManualResolve.value = false;
        showConflictModal.value = false;
        conflictInfo.value = null;
        showToast('冲突已手动解决，合并 ' + merged.length + ' 条任务', 'success');
    }

    // --- Load org members ---
    // 数据源改为 GET /api/org-members（返回 [{id, name}]），不再从 tasks JSON 的 key 推导。
    // key 已从业务 name 改为字符串化整数 id，且 name 无法从 key 还原，必须走独立接口。
    async function loadOrgMembers() {
        if (!currentUser.value) return;
        try {
            const data = await taskApi.getOrgMembers();
            orgMembers.value = (data && data.members) ? data.members : [];
        } catch (e) {
            orgMembers.value = [];
        }
    }

    // id → name 反查：viewingMember/assignee 存字符串化 id，展示时还原业务 name
    function memberName(id) {
        if (id == null) return '';
        const key = String(id);
        const m = orgMembers.value.find(m => String(m.id) === key);
        return m ? m.name : key;
    }

    // ========== Sync & Auth ==========
    const syncStatus = ref('idle');  // 'idle' | 'syncing' | 'error'
    const lastSyncError = ref('');

    async function save() {
        if (!currentUser.value) return;
        const { userId, orgId } = currentUser.value;
        // 如果正在查看其他成员，禁止保存
        if (viewingMember.value) {
            showToast('查看他人日程时不可编辑', 'warning');
            return;
        }

        // 0. 先从 localStorage 读取当前版本信息
        const userKey = STORAGE_KEY_USER + '_' + userId;
        let localVersion = null;
        const saved = localStorage.getItem(userKey);
        if (saved) {
            try {
                const parsed = JSON.parse(saved);
                localVersion = parsed.version || null;
            } catch (e) { /* ignore */ }
        }

        // 1. 保存到 localStorage（包含版本信息）
        try {
            const wrapped = await wrapData(tasks.value, localVersion);
            localStorage.setItem(userKey, JSON.stringify(wrapped));
        } catch (e) {
            showToast('本地存储失败，请检查浏览器存储设置', 'error');
            return;
        }

        // 2. 异步同步到服务器（包含冲突检测）
        await syncToServer(userId, orgId);
    }

    // saveLocal 只保存到 localStorage（不发请求），供增量同步后更新本地缓存。
    // 保留 wrapData 重算 MD5 以检测本地修改（localModified = md5 !== baseMd5）。
    // syncTaskToServer 完成后会用服务端返回的 version 覆盖此处的客户端 version。
    async function saveLocal() {
        if (!currentUser.value) return;
        const userId = currentUser.value.userId;
        const userKey = STORAGE_KEY_USER + '_' + userId;
        let localVersion = null;
        const saved = localStorage.getItem(userKey);
        if (saved) {
            try { localVersion = JSON.parse(saved).version || null; } catch { /* ignore */ }
        }
        try {
            const wrapped = await wrapData(tasks.value, localVersion);
            localStorage.setItem(userKey, JSON.stringify(wrapped));
        } catch { /* ignore */ }
    }

    // 增量同步：只发单条任务到服务器，避免全量 PUT 的带宽浪费
    // method: 'PATCH' 更新 | 'POST' 新增 | 'DELETE' 删除
    async function syncTaskToServer(task, method) {
        if (!authToken.value) return null;
        try {
            let data;
            if (method === 'PATCH') {
                data = await taskApi.updateTask(task);
            } else if (method === 'POST') {
                data = await taskApi.addTask(task);
            } else if (method === 'DELETE') {
                data = await taskApi.deleteTask(task.id);
            }
            // 更新本地 version（服务端重新计算的）
            if (data && data.data && data.data.version) {
                const userId = currentUser.value.userId;
                const userKey = STORAGE_KEY_USER + '_' + userId;
                const saved = localStorage.getItem(userKey);
                if (saved) {
                    try {
                        const parsed = JSON.parse(saved);
                        parsed.version = data.data.version;
                        localStorage.setItem(userKey, JSON.stringify(parsed));
                    } catch { /* ignore */ }
                }
            }
            return data ? data.data : null;
        } catch (err) {
            console.error('[todo] 增量同步 HTTP 错误', err);
            throw err;
        }
    }

    async function syncToServer(userId, orgId) {
        if (!userId || !orgId) return;
        syncStatus.value = 'syncing';
        lastSyncError.value = '';

        try {
            // 1. 先从服务器拉取最新数据（用于冲突检测，强制刷新跳过缓存）
            let data = { orgs: {} };
            let serverVersion = null;
            const fetched = await fetchTasksJSON(true);
            if (fetched) {
                data = fetched;
                const org = (data.orgs || {})[orgId];
                if (org && org[userId]) {
                    const unwrapped = unwrapData(org[userId]);
                    serverVersion = unwrapped.version;
                }
            }

            // 2. 从 localStorage 读取本地版本信息
            const userKey = STORAGE_KEY_USER + '_' + userId;
            let localVersion = null;
            const saved = localStorage.getItem(userKey);
            if (saved) {
                try {
                    const parsed = JSON.parse(saved);
                    localVersion = parsed.version || null;
                } catch (e) { /* ignore */ }
            }

            // 3. 冲突检测
            const conflict = checkConflict(localVersion, serverVersion);
            if (conflict.hasConflict) {
                // 有冲突，需要用户处理
                syncStatus.value = 'error';
                lastSyncError.value = '数据冲突，请先解决冲突';
                showToast('数据冲突，请刷新页面解决冲突', 'error');
                return;
            }

            // 4. 无冲突，上传数据（包含版本信息）
            if (!data.orgs) data.orgs = {};
            if (!data.orgs[orgId]) data.orgs[orgId] = {};
            const wrapped = await wrapData(tasks.value, serverVersion);
            data.orgs[orgId][userId] = wrapped;
            data.lastUpdated = new Date().toISOString();

            await taskApi.putTasks(data);
            invalidateTasksJsonCache();
            // 上传成功后更新 localStorage version。
            // 直接用刚才上传的 wrapped.version（服务端 PUT 不重算 MD5，原样存储），而不是
            // buildVersion 客户端重算 — 客户端 JSON.stringify 的 key 顺序与服务端
            // json.Marshal 不同，重算会导致下次刷新时 localStorage md5 与服务端 md5 不匹配。
            localStorage.setItem(STORAGE_KEY_USER + '_' + userId, JSON.stringify({ version: wrapped.version, tasks: tasks.value }));
            syncStatus.value = 'idle';
        } catch (err) {
            syncStatus.value = 'error';
            lastSyncError.value = err.message || '未知错误';
            showToast('远程同步失败，数据已保存在本地', 'error');
        }
    }

    // --- User init ---
    // 认证校验委托给 auth store 的 checkAuth()，避免重复实现 /api/me 调用逻辑。
    // checkAuth() 会自动处理：401 清除认证 / 200 刷新 user / 网络错误离线兜底。
    // 未登录时路由守卫已拦截跳转 /login，此处不再弹窗。

    async function init() {
        if (await checkAuth()) {
            startTokenRefresh()
            await loadUserTasks()
            return
        }
        // 未登录 → 路由守卫已跳转登录页，此处不处理
    }

    // 每天自动校验 token，过期则清除认证状态
    // apiCall 全局 401 拦截会自动跳转登录页
    let tokenTimer = null;
    // TOKEN_REFRESH_INTERVAL_MS token 有效性定时校验间隔（24h）
    const TOKEN_REFRESH_INTERVAL_MS = 24 * 60 * 60 * 1000;
    function startTokenRefresh() {
        if (tokenTimer) clearInterval(tokenTimer);
        tokenTimer = setInterval(async () => {
            if (!authToken.value) return;
            const valid = await checkAuth()
            if (!valid) {
                showToast('登录已过期，请重新登录', 'error');
            }
        }, TOKEN_REFRESH_INTERVAL_MS);
    }

    // ========== Computed Properties ==========
    const hasConflict = computed(() => conflictDates.value.length > 0);

    // 冲突处理 UI 辅助 computed
    const conflictLocalTime = computed(() => {
        if (!conflictInfo.value || !conflictInfo.value.local) return '未知';
        const ts = conflictInfo.value.local.version?.timestamp;
        if (!ts) return '未知';
        return new Date(ts).toLocaleString('zh-CN');
    });
    const conflictServerTime = computed(() => {
        if (!conflictInfo.value || !conflictInfo.value.server) return '未知';
        const ts = conflictInfo.value.server.version?.timestamp;
        if (!ts) return '未知';
        return new Date(ts).toLocaleString('zh-CN');
    });
    const conflictLocalTasks = computed(() => {
        if (!conflictInfo.value || !conflictInfo.value.local) return [];
        return conflictInfo.value.local.tasks || [];
    });
    const conflictServerTasks = computed(() => {
        if (!conflictInfo.value || !conflictInfo.value.server) return [];
        return conflictInfo.value.server.tasks || [];
    });

    // 列表视图：按状态分组
    const listGroupedTasks = computed(() => {
        const groups = [
            { key: 'todo', label: '📋 待办', items: [] },
            { key: 'progress', label: '🔄 进行中', items: [] },
            { key: 'done', label: '✅ 已完成', items: [] },
        ];
        for (const task of tasks.value) {
            const g = groups.find(g => g.key === task.status);
            if (g) g.items.push(task);
        }
        return groups;
    });

    // 四象限分类：重要=priority=high，紧急=due在3天内或已逾期
    const quadrantTasks = computed(() => {
        const qs = [
            { key: 'q1', icon: '🔥', label: '重要且紧急', items: [] },
            { key: 'q2', icon: '📅', label: '重要不紧急', items: [] },
            { key: 'q3', icon: '⚡', label: '紧急不重要', items: [] },
            { key: 'q4', icon: '🌱', label: '不重要不紧急', items: [] },
        ];
        for (const task of tasks.value) {
            // 四象限仅展示未完成任务（status === 'done' 的任务不进入象限）
            if (task.status === 'done') continue
            const important = task.priority === 'high';
            let urgent = false;
            if (task.due) {
                const d = new Date(task.due);
                if (!isNaN(d.getTime())) {
                    const today = new Date(); today.setHours(0, 0, 0, 0);
                    urgent = (d - today) <= 3 * 86400000;
                }
            }
            if (important && urgent) qs[0].items.push(task);
            else if (important && !urgent) qs[1].items.push(task);
            else if (!important && urgent) qs[2].items.push(task);
            else qs[3].items.push(task);
        }
        return qs;
    });

    // 根据视图模式返回对应任务列表
    const displayTasks = computed(() => {
        if (viewMode.value === 'personal') {
            return tasks.value;
        }
        // 组织视图：加载所有成员任务
        return orgTasks.value;
    });

    // orgTasks → imported from todoStore

    const editingMaxDate = computed(() => {
        const today = formatDate(new Date());
        return today;
    });

    const todayStr = computed(() => {
        return formatDate(new Date());
    });

    const calTitle = computed(() => {
        const ws = currentWeekStart.value;
        return `${ws.getFullYear()}年 第${getWeekNumber(ws)}周`;
    });

    const weekDays = computed(() => {
        const result = [];
        const weekdayNames = ['一', '二', '三', '四', '五', '六', '日'];
        const today = new Date();
        today.setHours(0, 0, 0, 0);
        for (let i = 0; i < 7; i++) {
            const d = new Date(currentWeekStart.value);
            d.setDate(d.getDate() + i);
            // currentWeekStart 是周一，i=0→周一→weekdayNames[0]='一'
            result.push({
                name: weekdayNames[i],
                month: d.getMonth() + 1,
                date: d.getDate(),
                dateStr: formatDate(d),
                isToday: formatDate(d) === formatDate(today),
                isWeekend: d.getDay() === 0 || d.getDay() === 6,
            });
        }
        return result;
    });

    const donePercent = computed(() => {
        if (tasks.value.length === 0) return 0;
        return Math.round(tasks.value.filter(t => t.status === 'done').length / tasks.value.length * 100);
    });

    const overdueCount = computed(() => {
        return tasks.value.filter(t => t.status !== 'done' && t.status !== 'conflict' && isOverdue(t)).length;
    });

    const postponedCount = computed(() => {
        return tasks.value.filter(t => (t.postponedCount || 0) > 0 && t.status !== 'done').length;
    });

    const conflictDates = computed(() => {
        const config = { conflictThreshold: 10 };
        const dailyTasks = {};
        const taskList = tasks.value;
        for (const t of taskList) {
            if (t.status === 'conflict') continue;
            if (t.scheduled) {
                if (!dailyTasks[t.scheduled]) dailyTasks[t.scheduled] = [];
                dailyTasks[t.scheduled].push(t);
            }
        }
        const result = [];
        for (const date in dailyTasks) {
            if (dailyTasks[date].length >= config.conflictThreshold) {
                result.push({ date, tasks: dailyTasks[date] });
            }
        }
        return result;
    });

    /** 当前周内可见的甘特任务 */
    const ganttTasks = computed(() => {
        const ws = currentWeekStart.value;
        const we = new Date(ws);
        we.setDate(we.getDate() + 6);
        const wsStr = formatDate(ws);
        const weStr = formatDate(we);
        return tasks.value.filter(t => {
            if (t.status === 'conflict') return false;
            if (!t.scheduled) return false;
            const end = t.due || t.scheduled;
            // 只显示与当前周有实际重叠的任务
            return t.scheduled <= weStr && end >= wsStr;
        });
    });

    const ganttTaskCount = computed(() => ganttTasks.value.length);

    /** 甘特图行打包：将时间不重叠的任务分配到同一轨道 */
    const ganttTracks = computed(() => {
        const tasks = ganttTasks.value;
        if (!tasks.length) return [];
        // 按开始时间排序，开始时间相同则按结束时间排序
        const sorted = [...tasks].sort((a, b) => {
            const aStart = a.scheduled || '';
            const bStart = b.scheduled || '';
            if (aStart !== bStart) return aStart.localeCompare(bStart);
            const aEnd = (a.due || a.scheduled || '');
            const bEnd = (b.due || b.scheduled || '');
            return aEnd.localeCompare(bEnd);
        });
        // 贪心分配轨道：每个轨道记录最后一个任务的结束日期
        const tracks = []; // [{ tasks: [task], lastEnd: 'YYYY-MM-DD' }]
        for (const task of sorted) {
            const taskStart = task.scheduled || '';
            const taskEnd = task.due || task.scheduled || '';
            let placed = false;
            for (const track of tracks) {
                if (track.lastEnd < taskStart) {
                    track.tasks.push(task);
                    track.lastEnd = taskEnd;
                    placed = true;
                    break;
                }
            }
            if (!placed) {
                tracks.push({ tasks: [task], lastEnd: taskEnd });
            }
        }
        return tracks.map(t => t.tasks);
    });

    // ========== Calendar & Gantt Utilities ==========
    function getWeekStart(date) {
        const d = new Date(date);
        const day = d.getDay();
        const diff = day === 0 ? -6 : 1 - day;
        d.setDate(d.getDate() + diff);
        d.setHours(0, 0, 0, 0);
        return d;
    }

    function getWeekNumber(date) {
        const start = new Date(date.getFullYear(), 0, 1);
        const ws = getWeekStart(date);
        return Math.ceil(((ws - start) / 86400000 + start.getDay() + 1) / 7);
    }

    function formatDate(d) {
        const y = d.getFullYear();
        const m = String(d.getMonth() + 1).padStart(2, '0');
        const day = String(d.getDate()).padStart(2, '0');
        return `${y}-${m}-${day}`;
    }

    function isOverdue(task) {
        if (!task.due) return false;
        if (task.status === 'done') return false;
        const today = new Date();
        today.setHours(0, 0, 0, 0);
        return new Date(task.due + 'T00:00:00') < today && task.progress < 100;
    }

    // 计算逾期天数（向上取整）。未逾期返回 0。
    function overdueDays(task) {
        if (!isOverdue(task)) return 0;
        const today = new Date();
        today.setHours(0, 0, 0, 0);
        const due = new Date(task.due + 'T00:00:00');
        return Math.ceil((today.getTime() - due.getTime()) / 86400000);
    }

    function dayOffset(startStr, dateStr) {
        const start = new Date(startStr + 'T00:00:00');
        const d = new Date(dateStr + 'T00:00:00');
        return Math.round((d - start) / 86400000);
    }

    function priorityLabel(p) { return priorityLabelMap[p] || '中'; }
    function statusIcon(s) { return statusIconMap[s] || '❓'; }
    function getStatusLabel(s) {
        const col = columns.find(c => c.key === s);
        return col ? col.label : s;
    }

    // --- Task getters ---
    function getTasksByStatus(status) {
        return tasks.value.filter(t => t.status === status);
    }
    function countByStatus(status) {
        return tasks.value.filter(t => t.status === status).length;
    }
    function getTasksForDate(dateStr) {
        return tasks.value.filter(t => {
            if (t.status === 'done' || t.status === 'conflict') return false;
            if (!t.scheduled) return false;
            const start = t.scheduled;
            const end = t.due || t.scheduled;
            return dateStr >= start && dateStr <= end;
        });
    }

    /** 甘特条定位样式：在7列日期区域内按百分比定位 */
    function ganttBarStyleV2(task) {
        const ws = currentWeekStart.value;
        const we = new Date(ws);
        we.setDate(we.getDate() + 6);
        const start = new Date(task.scheduled + 'T00:00:00');
        const end = task.due ? new Date(task.due + 'T00:00:00') : start;
        let visStart = start < ws ? ws : start;
        let visEnd = end > we ? we : end;
        // 天数偏移 (0~6)
        const startDay = Math.round((visStart.getTime() - ws.getTime()) / 86400000);
        const endDay = Math.round((visEnd.getTime() - ws.getTime()) / 86400000);
        // CSS width/margin % 都是相对父容器总宽度，所以统一除以 7
        // 每个bar两侧各留 gap (约0.4% ≈ 3-5px)，使相邻任务有视觉间隔
        const GAP_PCT = 0.4;
        const leftPct = (startDay / 7) * 100 + GAP_PCT;
        const daySpan = Math.min(endDay, 6) - startDay + 1;
        const rawWidthPct = (daySpan / 7) * 100;
        const widthPct = Math.max(rawWidthPct - GAP_PCT * 2, 2);
        return {
            left: leftPct.toFixed(1) + '%',
            width: widthPct.toFixed(1) + '%',
            maxWidth: (100 - leftPct).toFixed(1) + '%',
        };
    }

    function formatDateShort(d) {
        if (!d) return '';
        const dt = new Date(d + 'T00:00:00');
        return (dt.getMonth() + 1) + '/' + dt.getDate();
    }

    // --- Tab ---
    function switchTab(tab) {
        activeTab.value = tab;
    }

    // --- Drag & Drop ---
    function onDragStart(e, task) {
        // 只读模式：禁止拖拽
        if (viewingMember.value) {
            e.preventDefault();
            showToast('查看他人日程时不可编辑', 'warning');
            return;
        }
        draggingTaskId.value = task.id;
        e.dataTransfer.setData('taskId', task.id.toString());
        e.dataTransfer.effectAllowed = 'move';
    }

    function onBoardDrop(e, targetStatus) {
        e.preventDefault();
        // 只读模式：禁止放下
        if (viewingMember.value) {
            showToast('查看他人日程时不可编辑', 'warning');
            draggingTaskId.value = null;
            return;
        }
        const taskId = e.dataTransfer.getData('taskId');
        if (!taskId) return;
        const task = tasks.value.find(t => String(t.id) === taskId);
        if (task && task.status !== targetStatus) {
            task.status = targetStatus;
            if (targetStatus === 'progress') {
                const today = formatDate(new Date());
                task.scheduled = today;
                if (!task.due || task.due < today) {
                    task.due = today;
                }
            }
            // 拖拽到"已完成"时，进度自动设为100%
            if (targetStatus === 'done') {
                task.progress = 100;
            }
            // 增量同步：拖拽改状态只发这一条任务
            syncTaskToServer(task, 'PATCH').catch(err => {
                console.error('[todo] 拖拽同步失败，回退到全量同步', err);
                save();
            });
            saveLocal();
            showToast(`已移动到 ${getStatusLabel(targetStatus)}`, 'success');
        }
        draggingTaskId.value = null;
    }

    function onCalendarDragOver(dateStr) {
        // 只读模式：禁止拖拽悬停
        if (viewingMember.value) return;
        calendarDragOverDate.value = dateStr;
    }
    function onCalendarDragLeave() {
        if (viewingMember.value) return;
        calendarDragOverDate.value = null;
    }
    function onCalendarDrop(e, dateStr) {
        e.preventDefault();
        // 只读模式：禁止放下
        if (viewingMember.value) {
            showToast('查看他人日程时不可编辑', 'warning');
            calendarDragOverDate.value = null;
            draggingTaskId.value = null;
            return;
        }
        const today = formatDate(new Date());
        if (dateStr < today) {
            showToast('无法将任务安排到过往日期', 'error');
            calendarDragOverDate.value = null;
            draggingTaskId.value = null;
            return;
        }
        const taskId = e.dataTransfer.getData('taskId');
        if (!taskId) return;
        const task = tasks.value.find(t => String(t.id) === taskId);
        if (task) {
            task.scheduled = dateStr;
            if (dateStr >= today) {
                task.postponedFrom = null;
                task.postponedCount = 0;
                task.confirmedDate = null;
                task.autoPostponed = false;
            }
            save();
            showToast(`已安排到 ${dateStr}`, 'success');
        }
        calendarDragOverDate.value = null;
        draggingTaskId.value = null;
    }

    // --- Confirm Task ---
    function confirmTask(taskId) {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        const task = tasks.value.find(t => t.id === taskId);
        if (task) {
            task.confirmedDate = formatDate(new Date());
            task.status = 'done';
            task.progress = 100;
            task.postponedFrom = null;
            task.postponedCount = 0;
            task.autoPostponed = false;
            save();
            showToast('任务已确认完成', 'success');
        }
    }

    // --- Calendar navigation ---
    function changeWeek(delta) {
        const d = new Date(currentWeekStart.value);
        d.setDate(d.getDate() + delta * 7);
        currentWeekStart.value = d;
    }
    function goToThisWeek() {
        currentWeekStart.value = getWeekStart(new Date());
    }

    // --- Add task from calendar ---
    function addTaskToCalendar(dateStr) {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        const title = (newTaskForDate.value[dateStr] || '').trim();
        if (!title) return;
        const maxId = tasks.value.reduce((m, t) => Math.max(m, t.id || 0), 0);
        const task = {
            id: maxId + 1,
            title,
            status: 'progress',
            priority: 'medium',
            progress: 0,
            assignee: String(currentUser.value.userId),
            due: dateStr,
            scheduled: dateStr,
            createdAt: formatDate(new Date()),
            confirmedDate: null,
            postponedFrom: null,
            postponedCount: 0,
            autoPostponed: false,
        };
        tasks.value.push(task);
        newTaskForDate.value[dateStr] = '';
        save();
        showToast('任务已添加到日历', 'success');
    }

    // ========== Task CRUD ==========
    function toggleTaskDone(task) {
        if (!currentUser.value || viewingMember.value) return;
        if (task.status === 'done') {
            task.status = 'todo';
            // 不强制改 progress，保留用户原进度
        } else {
            task.status = 'done';
            task.progress = 100;
        }
        // 增量同步：只发这一条任务
        syncTaskToServer(task, 'PATCH').catch(err => {
            console.error('[todo] 增量同步失败，回退到全量同步', err);
            save();
        });
        saveLocal();
    }

    // 状态变更时同步进度（newStatus 由 @change 传入，避免 v-model 更新时序问题）
    function onStatusChange(task, newStatus) {
        if (newStatus === 'done') {
            task.progress = 100;
        } else if (newStatus === 'todo' && task.progress === 100) {
            task.progress = 0;
        }
    }

    // 进度变更联动：进度改为 100 时状态自动置为"已完成"；从 100 调低时若状态为"已完成"则回退为"进行中"
    function setEditingProgress(p) {
        editingTask.value.progress = p;
        if (p === 100) {
            editingTask.value.status = 'done';
        } else if (editingTask.value.status === 'done') {
            editingTask.value.status = 'progress';
        }
    }

    function listAddTask() {
        if (viewingMember.value) return;
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        const title = (listNewTitle.value || '').trim();
        if (!title) return;
        const todayStr = formatDate(new Date());
        const maxId = tasks.value.reduce((m, t) => Math.max(m, t.id || 0), 0);
        const task = {
            id: maxId + 1,
            title,
            content: '',
            status: 'todo',
            priority: 'medium',
            scheduled: todayStr,
            due: todayStr,
            progress: 0,
            assignee: String(currentUser.value.userId),
            postponedCount: 0,
            autoPostponed: true,
        };
        tasks.value.push(task);
        listNewTitle.value = '';
        save();
        showToast('已添加', 'success');
    }

    // --- Markdown 编辑 ---
    // 扩展 marked：支持 ==高亮== 语法 + highlight.js 代码块高亮
    if (typeof marked !== 'undefined') {
        const renderer = new Renderer();
        // 代码块高亮
        renderer.code = function (code, language) {
            let highlighted = '';
            if (typeof hljs !== 'undefined' && language && hljs.getLanguage(language)) {
                highlighted = hljs.highlight(code, { language: language }).value;
            } else if (typeof hljs !== 'undefined') {
                highlighted = hljs.highlightAuto(code).value;
            } else {
                highlighted = code;
            }
            const langClass = language ? 'language-' + language : '';
            return '<pre><code class="hljs ' + langClass + '">' + highlighted + '</code></pre>';
        };
        // ==高亮== 语法
        marked.use({
            extensions: [{
                name: 'mark',
                level: 'inline',
                start(src) { return src.indexOf('=='); },
                tokenizer(src) {
                    const rule = /^==(.+?)==/;
                    const match = rule.exec(src);
                    if (match) {
                        return {
                            type: 'mark',
                            raw: match[0],
                            text: match[1],
                        };
                    }
                },
                renderer(token) {
                    return '<mark>' + token.text + '</mark>';
                }
            }],
            renderer: renderer
        });
    }

    function toggleListGroup(key) {
        collapsedGroups[key] = !collapsedGroups[key];
    }

    function toggleListView() {
        listViewMode.value = listViewMode.value === 'status' ? 'quadrant' : 'status';
    }

    function addTask(status) {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        const title = (newTaskTitle.value[status] || '').trim();
        if (!title) return;
        const todayStr = formatDate(new Date());
        isCreating.value = true;
        editingTask.value = {
            id: null,  // 创建时 id 为 null，confirmCreate 中会分配
            title,
            content: '',
            status,
            priority: 'medium',
            progress: 0,
            scheduled: todayStr,
            due: todayStr,
            assignee: String(currentUser.value.userId),
            postponedCount: 0,
            autoPostponed: false,
        };
        editingDateRange.value = [todayStr, todayStr];
        showModal.value = true;
    }

    // Handle date range picker change (create & edit)
    function onEditingDateRangeChange(dates) {
        if (dates && dates.length === 2) {
            editingTask.value.scheduled = dates[0];
            editingTask.value.due = dates[1];
        } else {
            editingTask.value.scheduled = null;
            editingTask.value.due = null;
        }
    }

    function confirmCreate() {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        if (!editingTask.value.scheduled) {
            showToast('请选择计划日期', 'error');
            return;
        }
        if (!editingTask.value.due) {
            showToast('请选择截止日期', 'error');
            return;
        }
        if (editingTask.value.scheduled > editingTask.value.due) {
            showToast('计划日期不能晚于截止日期', 'error');
            return;
        }
        const maxId = tasks.value.reduce((m, t) => Math.max(m, t.id || 0), 0);
        const task = {
            id: maxId + 1,
            title: editingTask.value.title,
            content: editingTask.value.content || '',
            status: editingTask.value.status,
            priority: editingTask.value.priority || 'medium',
            progress: editingTask.value.progress || 0,
            assignee: editingTask.value.assignee || String(currentUser.value.userId),
            due: editingTask.value.due,
            scheduled: editingTask.value.scheduled,
            createdAt: formatDate(new Date()),
            confirmedDate: null,
            postponedFrom: null,
            postponedCount: 0,
            autoPostponed: false,
        };
        tasks.value.push(task);
        newTaskTitle.value[editingTask.value.status] = '';
        showModal.value = false;
        // 增量同步：POST 单条新任务
        syncTaskToServer(task, 'POST').catch(err => {
            console.error('[todo] 新增同步失败，回退到全量同步', err);
            save();
        });
        saveLocal();
        showToast('任务已创建', 'success');
    }

    function editTask(task) {
        // 只读模式禁止编辑
        if (viewingMember.value) return;
        isCreating.value = false;
        editingTask.value = { ...task };
        // Initialize date range picker & ensure [scheduled <= due]
        if (task.scheduled && task.due) {
            const start = task.scheduled <= task.due ? task.scheduled : task.due;
            const end = task.scheduled <= task.due ? task.due : task.scheduled;
            editingDateRange.value = [start, end];
            // Sync editingTask to ensure correct order for validation
            editingTask.value.scheduled = start;
            editingTask.value.due = end;
        } else {
            editingDateRange.value = [];
        }
        showModal.value = true;
    }

    function saveEdit() {
        if (viewingMember.value) return;
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        const updatedTask = { ...editingTask.value };
        if (!updatedTask.scheduled) {
            showToast('请填写计划日期', 'error');
            return;
        }
        if (!updatedTask.due) {
            showToast('请填写截止日期', 'error');
            return;
        }
        if (updatedTask.scheduled > updatedTask.due) {
            showToast('计划日期不能晚于截止日期', 'error');
            return;
        }
        const idx = tasks.value.findIndex(t => t.id === updatedTask.id);
        if (idx !== -1) {
            if (updatedTask.status === 'done' || updatedTask.status === 'conflict') {
                updatedTask.confirmedDate = formatDate(new Date());
            }
            tasks.value.splice(idx, 1, updatedTask);
            // 增量同步：只发这一条任务，不发全部
            syncTaskToServer(updatedTask, 'PATCH').catch(err => {
                console.error('[todo] 增量同步失败，回退到全量同步', err);
                save();  // 增量失败回退全量
            });
            saveLocal();
            showToast('任务已更新', 'success');
        }
        showModal.value = false;
    }

    function deleteTask(id) {
        // 只读模式禁止删除
        if (viewingMember.value) return;
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        tasks.value = tasks.value.filter(t => t.id !== id);
        // 增量同步：DELETE 单条任务
        syncTaskToServer({ id }, 'DELETE').catch(err => {
            console.error('[todo] 删除同步失败，回退到全量同步', err);
            save();
        });
        saveLocal();
        showModal.value = false;
        showToast('任务已删除', 'warning');
    }

    // --- Import / Export ---
    function exportJSON() {
        const payload = {
            version: '2.0',
            lastUpdated: new Date().toISOString(),
            config: { dailyCapacity: 3, conflictThreshold: 4, autoSave: true },
            tasks: tasks.value
        };
        const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `tasks-${formatDate(new Date())}.json`;
        a.click();
        URL.revokeObjectURL(url);
        showToast('已导出 JSON', 'success');
    }

    function importJSON() {
        const input = document.createElement('input');
        input.type = 'file';
        input.accept = '.json';
        input.onchange = (e) => {
            const file = e.target.files[0];
            if (!file) return;
            const reader = new FileReader();
            reader.onload = (ev) => {
                try {
                    const data = JSON.parse(ev.target.result);
                    const unwrapped = unwrapData(data);
                    tasks.value = unwrapped.tasks;
                    save();
                    showToast('导入成功', 'success');
                } catch (err) {
                    showToast('导入失败: ' + err.message, 'error');
                }
            };
            reader.readAsText(file);
        };
        input.click();
    }

    function saveToFile() {
        save();
        exportJSON();
    }

    function clearAll() {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error');
            showGlobalLogin();
            return;
        }
        // assignee 存字符串化 id，比较与显示统一用 String(userId) + userName
        const userKey = String(currentUser.value.userId);
        const userName = currentUser.value.userName || userKey;
        const userTasks = tasks.value.filter(t => t.assignee === userKey);
        if (userTasks.length === 0) {
            showToast('你当前没有任务可清空', 'info');
            return;
        }
        if (!confirm(`确定要清空「${userName}」的所有任务（共 ${userTasks.length} 项）吗？此操作不可撤销！`)) return;
        tasks.value = tasks.value.filter(t => t.assignee !== userKey);
        save();
        showToast(`已清空「${userName}」的 ${userTasks.length} 项任务`, 'warning');
    }

    // --- Toast ---
    function showToast(msg, type) {
        const typeMap = { success: 'success', error: 'error', warning: 'warning', info: 'info' };
        ElNotification({
            message: msg,
            type: typeMap[type] || 'success',
            duration: 2000,
            position: 'bottom-right',
        });
    }

    onMounted(() => { init(); });

    // --- Watch viewingMember ---
    watch(viewingMember, async (newMemberId) => {
        if (!newMemberId) {
            // 切换回自己的日程
            await loadUserTasks();
            showToast('已切换回我的日程', 'success');
            return;
        }
        // 加载其他成员的日程（只读）
        const { orgId } = currentUser.value;
        const memberNameText = memberName(newMemberId);
        try {
            const data = await fetchTasksJSON();
            if (data) {
                const org = (data.orgs || {})[orgId];
                if (org && org[newMemberId] && Array.isArray(org[newMemberId].tasks)) {
                    tasks.value = org[newMemberId].tasks;
                    showToast(`正在查看 ${memberNameText} 的日程（只读）`, 'success');
                } else {
                    tasks.value = [];
                    showToast(`${memberNameText} 暂无任务`, 'warning');
                }
            }
        } catch (e) {
            tasks.value = [];
            showToast('加载成员日程失败', 'error');
        }
    });

    // ========== Sidebar ↔ Main selection sync ==========
    // When user clicks a task in the TodoSidebar, open the edit modal in the main view
    watch(activeTaskId, (id) => {
        if (id != null) {
            const task = tasks.value.find(t => t.id === id)
            if (task) editTask(task)
        }
    })

    // 关闭任务编辑弹窗：清除 showModal + 清除 activeTaskId
    // 必须清除 activeTaskId，否则再次点击同一任务时 watch 不触发（值未变化）
    function closeModal() {
        showModal.value = false
        activeTaskId.value = null
    }

    // Modal 键盘快捷键处理：Ctrl+S 自动保存（不关闭 panel），Ctrl+W 关闭 panel
    function handleModalKeydown(e: KeyboardEvent) {
        // 检测 Ctrl+S (Windows/Linux) 或 Cmd+S (Mac) - 保存
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
            e.preventDefault()
            e.stopPropagation()
            if (isCreating.value) {
                confirmCreate()
            } else {
                // saveEdit 内部会关闭 modal，我们需要保存但不关闭
                // 因此直接调用保存逻辑
                quickSaveEdit()
            }
            return
        }
        // 检测 Ctrl+W (Windows/Linux) 或 Cmd+W (Mac) - 关闭 panel
        if ((e.ctrlKey || e.metaKey) && e.key === 'w') {
            e.preventDefault()
            e.stopPropagation()
            closeModal()
            return
        }
    }

    // 快速保存：执行保存逻辑但不关闭 modal
    function quickSaveEdit() {
        if (viewingMember.value) return
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error')
            showGlobalLogin()
            return
        }
        const updatedTask = { ...editingTask.value }
        if (!updatedTask.scheduled) {
            showToast('请填写计划日期', 'error')
            return
        }
        if (!updatedTask.due) {
            showToast('请填写截止日期', 'error')
            return
        }
        if (updatedTask.scheduled > updatedTask.due) {
            showToast('计划日期不能晚于截止日期', 'error')
            return
        }
        const idx = tasks.value.findIndex(t => t.id === updatedTask.id)
        if (idx !== -1) {
            if (updatedTask.status === 'done' || updatedTask.status === 'conflict') {
                updatedTask.confirmedDate = formatDate(new Date())
            }
            tasks.value.splice(idx, 1, updatedTask)
            // 增量同步：只发这一条任务，不发全部
            syncTaskToServer(updatedTask, 'PATCH').catch(err => {
                console.error('[todo] 增量同步失败，回退到全量同步', err)
                save()  // 增量失败回退全量
            })
            saveLocal()
            showToast('任务已保存', 'success')
        }
        // 注意：不关闭 modal，保持 panel 打开
    }

    // ========== Expose to Template ==========
    return {
        columns, tasks, activeTab, draggingTaskId, calendarDragOverDate,
        listNewTitle, listViewMode, collapsedGroups, listGroupedTasks, quadrantTasks,
        toggleTaskDone, listAddTask, toggleListGroup, toggleListView,
        showModal, isCreating, editingTask, editingMaxDate, closeModal,
        handleModalKeydown,

        newTaskTitle, newTaskForDate, hasConflict, calTitle, weekDays, donePercent,
        todayStr, overdueCount, conflictDates,
        priorityLabel, statusIcon, getStatusLabel,
        getTasksByStatus, countByStatus, getTasksForDate,
        isOverdue, overdueDays, dayOffset, ganttTasks, ganttTracks, ganttTaskCount, ganttBarStyleV2, formatDateShort, switchTab,
        onDragStart, onBoardDrop,
        onStatusChange, setEditingProgress,
        onCalendarDragOver, onCalendarDragLeave, onCalendarDrop,
        confirmTask, addTaskToCalendar,
        changeWeek, goToThisWeek,
        addTask, editTask, saveEdit, deleteTask, confirmCreate,
        exportJSON, importJSON, saveToFile, clearAll, save,
        // Date range picker
        editingDateRange, onEditingDateRangeChange,
        editingTaskContent,
        // User
        currentUser,
        // Org members
        viewingMember, orgMembers, memberName,
        // Sync status
        syncStatus, lastSyncError,
        // Conflict resolution
        conflictInfo, showConflictModal, resolveConflict,
        conflictLocalTime, conflictServerTime,
        conflictLocalTasks, conflictServerTasks,
        // Manual conflict resolution
        showManualResolve, conflictTaskList, conflictChoices,
        openManualResolve, confirmManualResolve,
        selectAllLocal, selectAllServer,
        filteredConflictList, fieldLabel, formatField, typeLabel, showFields, diffClass,
        filterSeverity, showConfirmSummary, confirmSummary, executeMerge,
    };
}
