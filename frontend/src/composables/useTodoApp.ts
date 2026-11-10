// @ts-nocheck — 原生 JS 迁移代码，待 SFC 化时逐组件类型化（第3步）
import { ref, reactive, computed, watch, onMounted, onActivated, onDeactivated, nextTick } from 'vue'
import {
    tasks, showModal, isCreating, editingTask, editingDateRange,
    currentUser, authToken,
    viewingMember, orgMembers, syncStatus, lastSyncError,
    orgTasks, activeTaskId, conflictCard,
} from '../stores/todoStore'
import { checkAuth, registerTodoAppReset, openTabFn } from '../stores/indexStore'
import * as taskApi from '../api/tasks'
import { ElNotification } from 'element-plus'
import { useRouter } from 'vue-router'
import { baseName, extFromPath } from '../lib/common'
import {
    newTaskId, normalizeAssignees,
    getWeekStart, getWeekNumber, formatDate, formatDateShort, dayOffset,
    isOverdue, overdueDays, priorityLabel, statusIcon, getStatusLabel,
} from '../lib/todoUtils'
import { createTodoSync } from './todoSync'

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

    // --- Auto Status Update ---
    // 只计算延期天数，不自动修改状态（状态由用户手动管理）
    function autoUpdateStatus() {
        const today = formatDate(new Date());
        const todayDt = new Date(today + 'T00:00:00');
        for (const task of tasks.value) {
            if (task.status === 'done') continue;
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

    // --- 同步层（服务端权威 revision + 乐观锁 + 轮询对账，见 todoSync.ts） ---
    const sync = createTodoSync({
        tasks, currentUser, authToken, syncStatus, lastSyncError,
        viewingMember, showModal, conflictCard, orgMembers,
        showToast, autoUpdateStatus, loadOrgMembers,
    });
    const {
        loadUserTasks, saveLocal, reloadFromServer, applyMutation,
        resolveConflictCard, discardConflictCard,
        pollRevision, startRevisionPolling, stopRevisionPolling,
    } = sync;

    // 身份切换（登出/换账号/切换组织）时清空任务请求缓存（todoSync 内部 3 秒 TTL）
    registerTodoAppReset(sync.invalidateCache);

    // KeepAlive：TodoView 失活暂停轮询，激活恢复；页面切回前台立即对账一次
    onActivated(startRevisionPolling);
    onDeactivated(stopRevisionPolling);

    // --- Load tasks for current user ---
    // conflict state → imported from todoStore

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
        return tasks.value.filter(t => t.status !== 'done' && isOverdue(t)).length;
    });

    const postponedCount = computed(() => {
        return tasks.value.filter(t => (t.postponedCount || 0) > 0 && t.status !== 'done').length;
    });

    const conflictDates = computed(() => {
        const config = { conflictThreshold: 10 };
        const dailyTasks = {};
        const taskList = tasks.value;
        for (const t of taskList) {
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

    // --- Task getters ---
    function getTasksByStatus(status) {
        return tasks.value.filter(t => t.status === status);
    }
    function countByStatus(status) {
        return tasks.value.filter(t => t.status === status).length;
    }
    function getTasksForDate(dateStr) {
        return tasks.value.filter(t => {
            if (t.status === 'done') return false;
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
            // 增量同步：拖拽改状态只发这一条任务（乐观锁，409 弹对比卡）
            applyMutation('PATCH', task, ['status', 'scheduled', 'due', 'progress']);
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
            saveLocal();
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
            saveLocal();
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
        const task = {
            id: newTaskId(),
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
        saveLocal();
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
        applyMutation('PATCH', task, ['status', 'progress']);
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

    // --- 关联文件（todo ↔ file 联动）---
    // 编辑弹窗中通过 FilePickerModal（文件树选择器）增删关联文件
    function removeEditingFile(i) {
        (editingTask.value.files || []).splice(i, 1);
    }

    // 关联文件 chip 点击 → 打开文件预览（文件浏览页新 tab；openTabFn 由 useIndexApp 注册）
    const fileRouter = useRouter();
    function openLinkedFile(path) {
        const name = baseName(path);
        if (openTabFn) openTabFn(path, name, extFromPath(name), 0);
        fileRouter.push('/file');
    }

    // 文件树选择器（FilePickerModal）：打开 / 确认批量追加（去重、上限）
    const showFilePicker = ref(false);

    function openFilePicker() {
        showFilePicker.value = true;
    }

    function confirmPickFiles(paths) {
        const files = editingTask.value.files || (editingTask.value.files = []);
        let added = 0;
        for (const p of paths || []) {
            if (files.includes(p)) continue;
            if (files.length >= 20) { showToast('关联文件最多 20 个', 'error'); break; }
            files.push(p);
            added++;
        }
        showFilePicker.value = false;
        if (added) showToast(`已添加 ${added} 个关联文件`);
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
        const task = {
            id: newTaskId(),
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
        saveLocal();
        showToast('已添加', 'success');
    }

    // Markdown 渲染配置（==高亮== / 代码高亮）已收敛到 lib/markdownRenderer.ts，
    // 由 MarkdownPreview 统一装配，这里不再做全局 marked 定制。

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
        const task = {
            id: newTaskId(),
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
            files: editingTask.value.files || [],
        };
        tasks.value.push(task);
        newTaskTitle.value[editingTask.value.status] = '';
        showModal.value = false;
        applyMutation('POST', task, []);
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
            if (updatedTask.status === 'done' ) {
                updatedTask.confirmedDate = formatDate(new Date());
            }
            tasks.value.splice(idx, 1, updatedTask);
            // 增量同步：只发这一条任务（编辑保存字段多，冲突时对比卡全字段展示）
            applyMutation('PATCH', updatedTask, ['title', 'content', 'status', 'priority', 'scheduled', 'due', 'progress', 'assignee', 'postponedCount', 'autoPostponed', 'sortOrder', 'files']);
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
        applyMutation('DELETE', { id }, []);
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

    // 导入 JSON：合并语义——服务器已有的（同 id）跳过，新任务逐条 POST 上传
    function importJSON() {
        const input = document.createElement('input');
        input.type = 'file';
        input.accept = '.json';
        input.onchange = (e) => {
            const file = e.target.files[0];
            if (!file) return;
            const reader = new FileReader();
            reader.onload = async (ev) => {
                try {
                    const data = JSON.parse(ev.target.result);
                    // 兼容两种导出格式：{tasks:[...]} 包装 或 纯任务数组
                    const imported = Array.isArray(data) ? data : (Array.isArray(data?.tasks) ? data.tasks : null);
                    if (!imported) throw new Error('无法识别的格式（需要任务数组）');
                    const existing = new Set(tasks.value.map(t => String(t.id)));
                    const fresh = imported.filter(t => t && t.id && !existing.has(String(t.id)));
                    if (!fresh.length) {
                        showToast('导入的任务都已存在，未新增', 'info');
                        return;
                    }
                    for (const t of fresh) {
                        tasks.value.push(t);
                    }
                    for (const t of fresh) {
                        await applyMutation('POST', t, []);
                    }
                    showToast(`导入成功，新增 ${fresh.length} 条（跳过 ${imported.length - fresh.length} 条已存在）`, 'success');
                } catch (err) {
                    showToast('导入失败: ' + err.message, 'error');
                }
            };
            reader.readAsText(file);
        };
        input.click();
    }

    function saveToFile() {
        saveLocal();
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
        // 先记录待删 id（filter 之后原数组引用变化），再逐条同步服务器
        const removedIds = userTasks.map(t => t.id);
        tasks.value = tasks.value.filter(t => t.assignee !== userKey);
        for (const id of removedIds) {
            applyMutation('DELETE', { id }, []);
        }
        saveLocal();
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
        // 注意：GET /api/tasks 只返回当前用户自己的任务（RBAC 收紧），
        // 查看成员须走专用端点 /api/tasks/user/{id}，且不走 3 秒缓存（每次现拉）
        const { orgId } = currentUser.value;
        const memberNameText = memberName(newMemberId);
        try {
            const data = await taskApi.getMemberTasks(newMemberId);
            const org = (data.orgs || {})[orgId];
            if (org && org[newMemberId] && Array.isArray(org[newMemberId].tasks)) {
                tasks.value = org[newMemberId].tasks;
                showToast(`正在查看 ${memberNameText} 的日程（只读）`, 'success');
            } else {
                tasks.value = [];
                showToast(`${memberNameText} 暂无任务`, 'warning');
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

    // Modal 键盘快捷键处理：Ctrl+S 自动保存（不关闭 panel），Escape 关闭 panel
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
        // 检测 Escape - 关闭 panel
        // 注意：Ctrl+W 无法拦截（浏览器安全策略），使用 Escape 替代
        if (e.key === 'Escape') {
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
            if (updatedTask.status === 'done' ) {
                updatedTask.confirmedDate = formatDate(new Date())
            }
            tasks.value.splice(idx, 1, updatedTask)
            applyMutation('PATCH', updatedTask, ['status', 'scheduled', 'due', 'progress'])
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
        // 关联文件（todo ↔ file 联动）
        removeEditingFile, openLinkedFile,
        showFilePicker, openFilePicker, confirmPickFiles,
        onCalendarDragOver, onCalendarDragLeave, onCalendarDrop,
        confirmTask, addTaskToCalendar,
        changeWeek, goToThisWeek,
        addTask, editTask, saveEdit, deleteTask, confirmCreate,
        exportJSON, importJSON, saveToFile, clearAll, saveLocal,
        // Date range picker
        editingDateRange, onEditingDateRangeChange,
        editingTaskContent,
        // User
        currentUser,
        // Org members
        viewingMember, orgMembers, memberName,
        // Sync status
        syncStatus, lastSyncError,
        // 任务级冲突（409 对比卡）
        conflictCard, resolveConflictCard, discardConflictCard,
    };
}
