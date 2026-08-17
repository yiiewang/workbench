<script>
import { setupTodoApp } from '../composables/useTodoApp'
import TaskItem from '../components/todo/TaskItem.vue'
import MarkdownEditor from '../components/todo/MarkdownEditor.vue'

export default {
  components: { TaskItem, MarkdownEditor },
  setup() {
    return setupTodoApp()
  }
}
</script>

<template>
    <div id="todo-app" v-cloak>
        <!-- Main Panel: header + stats + conflict + tabs + content as one unified block -->
        <div class="main-panel">
            <div class="header">
                <h1>📋 待办事项看板</h1>
                <div class="header-actions">
                    <div class="user-info-group">
                        <template v-if="currentUser">
                            <span class="user-badge">👤 {{ currentUser.userId }} ({{ currentUser.orgId }})</span>
                            <!-- 成员切换器 -->
                            <select v-if="orgMembers.length > 1" v-model="viewingMember" class="member-select">
                                <option :value="null">📋 我的日程</option>
                                <option v-for="m in orgMembers" :key="m" :value="m">{{ m === currentUser.userId ? '👤' :
                                    '👥' }} {{ m }}</option>
                            </select>
                            <span v-if="syncStatus === 'syncing'" class="user-badge status-syncing">⏳ 同步中...</span>
                            <span v-if="syncStatus === 'error'" class="user-badge status-error"
                                :title="lastSyncError">⚠️ 同步失败</span>
                        </template>
                    </div>
                    <!-- 只读模式下隐藏操作按钮 -->
                    <div v-if="!viewingMember" class="action-buttons">
                        <button class="btn btn-secondary" @click="importJSON">📂 导入</button>
                        <button class="btn btn-danger" @click="clearAll">🗑 清空</button>
                        <button class="btn btn-success" @click="saveToFile">💾 保存</button>
                    </div>
                </div>
            </div>

            <!-- 只读模式提示条 -->
            <div class="readonly-banner" v-if="viewingMember">
                <span class="lock-icon">🔒</span>
                <span>正在查看 <strong>{{ viewingMember }}</strong> 的日程</span>
                <span class="readonly-tag">只读</span>
                <button class="btn-return" @click="viewingMember = null">✕ 返回</button>
            </div>

            <!-- Global Stats Summary -->
            <div class="progress-section" v-if="tasks.length > 0">
                <div class="progress-badges">
                    <span class="progress-badge">📋 共 {{ tasks.length }} 项</span>
                    <span class="progress-badge todo">待办 {{ countByStatus('todo') }}</span>
                    <span class="progress-badge progress">进行中 {{ countByStatus('progress') }}</span>
                    <span class="progress-badge done">完成 {{ countByStatus('done') }}</span>
                    <span v-if="overdueCount > 0" class="progress-badge overdue">⚠ 逾期 {{ overdueCount }}</span>
                    <span v-if="conflictDates.length > 0" class="progress-badge conflict">
                        ⚠ 冲突 {{ conflictDates.length }} 天
                    </span>
                </div>
                <div class="progress-text">
                    完成率 <strong>{{ donePercent }}%</strong>
                </div>
            </div>

            <div class="tabs">
                <button class="tab-btn" :class="{ active: activeTab === 'quadrant' }" @click="switchTab('quadrant')">▦
                    四象限</button>
                <button class="tab-btn" :class="{ active: activeTab === 'board' }" @click="switchTab('board')">📊
                    看板</button>
                <button class="tab-btn" :class="{ active: activeTab === 'calendar' }" @click="switchTab('calendar')">📅
                    日历</button>
            </div>

            <!-- Board Tab -->
            <div class="tab-content" v-show="activeTab === 'board'">
                <!-- Kanban Board -->
                <div class="board">
                    <div class="column" v-for="col in columns" :key="col.key" :class="'col-' + col.key"
                        @dragover.prevent @drop="onBoardDrop($event, col.key)">
                        <div class="column-header">
                            <span>{{ col.icon }} {{ col.label }}</span>
                            <span class="column-count">{{ getTasksByStatus(col.key).length }}</span>
                        </div>
                        <div class="tasks-scroll">
                            <div v-if="getTasksByStatus(col.key).length === 0" class="empty-state">暂无任务</div>
                            <task-item
                                v-for="task in getTasksByStatus(col.key)"
                                :key="task.id"
                                :task="task"
                                variant="card"
                                :draggable="!viewingMember"
                            />
                        </div>
                        <div class="task-input" v-if="col.key !== 'conflict' && !viewingMember">
                            <div class="input-group">
                                <input type="text" :placeholder="'添加' + col.label + '任务...'"
                                    v-model="newTaskTitle[col.key]" @keyup.enter="addTask(col.key)">
                                <button class="btn btn-primary" @click="addTask(col.key)">+</button>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- Quadrant Tab: 四象限视图（默认） -->
            <div class="tab-content" v-show="activeTab === 'quadrant'">
                <div class="todo-list-view">
                    <div class="list-add-row" v-if="!viewingMember">
                        <input type="text" class="list-add-input" v-model="listNewTitle" placeholder="输入任务名称..."
                            @keyup.enter="listAddTask">
                        <button class="btn btn-primary" @click="listAddTask">添加</button>
                    </div>
                    <div v-if="tasks.length === 0" class="empty-list">暂无任务，输入上方添加</div>
                    <div v-else class="quadrant-grid">
                        <div v-for="q in quadrantTasks" :key="q.key" class="quadrant-cell" :class="q.key">
                            <div class="quadrant-header">
                                <span>{{ q.icon }} {{ q.label }}</span>
                                <span class="q-count">{{ q.items.length }}</span>
                            </div>
                            <div class="quadrant-body" v-if="q.items.length > 0">
                                <task-item :task="task" v-for="task in q.items" :key="task.id"></task-item>
                            </div>
                            <div v-else class="quadrant-body quadrant-empty">暂无任务</div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- Calendar Tab - Gantt View -->
            <div class="tab-content" v-show="activeTab === 'calendar'">
                <div class="calendar-header">
                    <button class="cal-nav" @click="changeWeek(-1)">‹</button>
                    <h2>{{ calTitle }}</h2>
                    <div class="cal-nav-container">
                        <button class="cal-nav today" @click="goToThisWeek">本周</button>
                        <button class="cal-nav" @click="changeWeek(1)">›</button>
                    </div>
                </div>
                <div class="calendar-scroll-wrapper">
                    <div class="week-view">
                        <!-- Row 1: Date headers (7 columns) -->
                        <div v-for="(day, i) in weekDays" :key="'h'+i" class="gantt-header-cell"
                            :class="{ today: day.isToday, weekend: day.isWeekend }">
                            <span>周{{ day.name }}</span>
                            <span class="day-num">{{ day.date }}</span>
                        </div>
                        <!-- Task tracks: each track = a row, bars in same track are non-overlapping -->
                        <template v-if="ganttTracks.length > 0">
                            <div v-for="(track, ti) in ganttTracks" :key="'track'+ti" class="gantt-grid-area">
                                <div v-for="task in track" :key="task.id" class="gantt-bar"
                                    :class="[task.status, { overdue: isOverdue(task) && task.status !== 'done' }]"
                                    :style="ganttBarStyleV2(task)" :draggable="!viewingMember"
                                    @dragstart="!viewingMember && onDragStart($event, task)"
                                    @click="!viewingMember && editTask(task)">
                                    <span class="bar-title">{{ task.title }}</span>
                                    <span class="progress-ring" :title="(task.progress || 0) + '% 完成'">
                                        <svg viewBox="0 0 20 20">
                                            <circle class="ring-bg" cx="10" cy="10" r="7" stroke-width="2.5" />
                                            <circle class="ring-fg" cx="10" cy="10" r="7" stroke-dasharray="43.98"
                                                :stroke-dashoffset="43.98 * (1 - (task.progress || 0) / 100)" />
                                        </svg>
                                    </span>
                                    <span v-if="isOverdue(task)" class="bar-status overdue">逾期</span>
                                    <span v-else-if="task.status === 'todo'" class="bar-status">待办</span>
                                    <span v-else-if="task.status === 'progress'" class="bar-status">进行中</span>
                                    <span v-else-if="task.status === 'done'" class="bar-status">已完成</span>
                                </div>
                            </div>
                        </template>
                        <!-- Empty state -->
                        <template v-else>
                            <div class="gantt-empty">
                                本周暂无任务，<a href="#" @click.prevent="switchTab('board')" class="gantt-empty-link">去看板添加</a>
                            </div>
                        </template>
                    </div>
                </div>
            </div>

            <!-- Unified Task Modal (Create & Edit) -->
            <div class="modal-mask" v-if="showModal" @click.self="closeModal">
                <div class="modal-content">
                    <div class="modal-header">
                        <h3>{{ isCreating ? '创建任务' : '编辑任务' }}</h3>
                        <button class="modal-close" @click="closeModal">×</button>
                    </div>

                    <!-- Main Body: left form sidebar + right markdown area -->
                    <div class="modal-body">
                        <!-- Left: Form Sidebar -->
                        <div class="modal-form-sidebar">
                            <div class="form-field">
                                <label>任务标题</label>
                                <input type="text" v-model="editingTask.title" :disabled="isCreating" placeholder="任务标题">
                            </div>
                            <div class="form-field" v-if="!isCreating">
                                <label>状态</label>
                                <select v-model="editingTask.status" @change="onStatusChange(editingTask, $event.target.value)">
                                    <option value="todo">待办</option>
                                    <option value="progress">进行中</option>
                                    <option value="done">已完成</option>
                                </select>
                            </div>
                            <div class="form-field">
                                <label>优先级</label>
                                <select v-model="editingTask.priority">
                                    <option value="high">高</option>
                                    <option value="medium">中</option>
                                    <option value="low">低</option>
                                </select>
                            </div>
                            <div class="form-field">
                                <label>进度</label>
                                <div class="progress-inline">
                                    <el-slider v-model="editingTask.progress" :min="0" :max="100" :show-tooltip="false" color="#2e7d32" />
                                    <span class="progress-pct">{{ editingTask.progress || 0 }}%</span>
                                </div>
                            </div>
                            <div class="form-field">
                                <label>时间范围 <span class="required">*</span></label>
                                <el-date-picker v-model="editingDateRange" type="daterange" range-separator="至"
                                    start-placeholder="计划" end-placeholder="截止" format="YYYY-MM-DD"
                                    value-format="YYYY-MM-DD" size="default"
                                    @change="onEditingDateRangeChange"></el-date-picker>
                            </div>
                            <div class="form-field" v-if="!isCreating">
                                <label>责任人</label>
                                <input type="text" v-model="editingTask.assignee" placeholder="责任人">
                            </div>
                            <!-- Postponed Info -->
                            <div class="postponed-info" v-if="!isCreating && editingTask.postponedCount > 0 && isOverdue(editingTask)"
                                style="margin-top:12px;padding:10px;border-radius:6px;font-size:12px;">
                                已延期 {{ editingTask.postponedCount }} 次<template v-if="editingTask.postponedFrom">，原计划: {{ editingTask.postponedFrom }}</template>
                            </div>
                        </div>

                        <!-- Right: Markdown Editor Area -->
                        <div class="md-area">
                            <markdown-editor v-model="editingTaskContent" />
                        </div>
                    </div>

                    <!-- Actions Bar: fixed bottom -->
                    <div class="modal-actions">
                        <button v-if="!isCreating" class="btn btn-danger" @click="deleteTask(editingTask.id)">删除</button>
                        <div v-else></div>
                        <button class="btn btn-secondary" @click="closeModal">取消</button>
                        <button class="btn btn-primary" @click="isCreating ? confirmCreate() : saveEdit()">{{ isCreating ? '创建' : '保存' }}</button>
                    </div>
                </div>
            </div>

        </div><!-- /main-panel -->

        <!-- Conflict Resolution Modal -->
        <div class="modal-mask" v-if="showConflictModal" @click.self="showConflictModal = false">
            <div class="modal-content" style="max-width:680px;height:auto;max-height:none;">
                <div class="modal-header">
                    <h3>⚠️ 检测到数据冲突</h3>
                </div>
                <div class="modal-body" style="display:block;padding:16px 24px;overflow:visible;">
                    <p style="font-size:13px;color:#666;margin:0 0 12px;">本地数据和服务器数据都存在修改，请选择处理方式：</p>
                    <div style="display:flex;gap:16px;">
                        <!-- 本地版本 -->
                        <div class="conflict-option" @click="resolveConflict('local')">
                            <div class="conflict-option-title">📱 保留本地版本</div>
                            <div class="conflict-option-meta">
                                修改时间：{{ conflictLocalTime }}<br>
                                任务数：{{ conflictLocalTasks.length }} 条
                            </div>
                        </div>
                        <!-- 服务器版本 -->
                        <div class="conflict-option" @click="resolveConflict('server')">
                            <div class="conflict-option-title">☁️ 保留服务器版本</div>
                            <div class="conflict-option-meta">
                                修改时间：{{ conflictServerTime }}<br>
                                任务数：{{ conflictServerTasks.length }} 条
                            </div>
                        </div>
                    </div>
                </div>
                <div class="modal-actions">
                    <button class="btn btn-secondary" @click="resolveConflict('merge')">智能合并</button>
                    <button class="btn btn-primary" @click="openManualResolve">🔍 手动解决</button>
                    <button class="btn btn-ghost" @click="showConflictModal = false">取消</button>
                </div>
            </div>
        </div>

        <!-- Manual Conflict Resolution Modal -->
        <div class="modal-mask" v-if="showManualResolve" @click.self="showManualResolve = false">
            <div class="modal-content"
                style="max-width: 1100px; height: 80vh; max-height: 80vh; display: flex; flex-direction: column;">
                <div class="modal-header" style="flex-shrink: 0;">
                    <h3>⚠️ 手动解决冲突</h3>
                    <button class="modal-close" @click="showManualResolve = false">×</button>
                </div>
                <div class="modal-body"
                    style="display:flex;flex-direction:column;flex:1;min-height:0;overflow:hidden;padding:12px 24px 16px;">
                    <!-- 统计栏 + 筛选 -->
                    <div style="display: flex; align-items: center; gap: 12px; padding: 4px 0 10px; font-size: 13px; color: #666; flex-shrink: 0; flex-wrap: wrap;">
                        <span>共 <strong>{{ conflictTaskList.length }}</strong> 条差异</span>
                        <span style="margin-left: auto; font-size: 12px; color: #999;">默认按修改时间预选，请核对：</span>
                        <div class="filter-btn-group">
                            <button class="btn btn-sm" :class="{ active: filterSeverity === 'all' }" @click="filterSeverity = 'all'">全部</button>
                            <button class="btn btn-sm" :class="{ active: filterSeverity === 'high' }" @click="filterSeverity = 'high'" style="color:#e53935;">🔴 高危</button>
                            <button class="btn btn-sm" :class="{ active: filterSeverity === 'medium' }" @click="filterSeverity = 'medium'" style="color:#fb8c00;">🟡 中危</button>
                            <button class="btn btn-sm" :class="{ active: filterSeverity === 'low' }" @click="filterSeverity = 'low'" style="color:#43a047;">🟢 低危</button>
                        </div>
                        <div style="display: flex; gap: 6px;">
                            <button class="btn btn-sm btn-secondary" @click="selectAllLocal">全选本地</button>
                            <button class="btn btn-sm btn-secondary" @click="selectAllServer">全选服务器</button>
                        </div>
                    </div>
                    <!-- 卡片列表 -->
                    <div style="flex:1;min-height:0;overflow-y:auto;">
                        <div class="diff-list">
                            <div v-for="row in filteredConflictList" :key="row.id" class="diff-card">
                                <!-- 标题栏 -->
                                <div class="diff-card-header">
                                    <span class="severity-dot" :class="'severity-'+row.severity"></span>
                                    <span style="flex:1;">{{ row.title }}</span>
                                    <span class="diff-type-tag" :class="row.type">{{ typeLabel(row) }}</span>
                                </div>
                                <!-- local-only: 单栏 + 选择 -->
                                <div v-if="row.type==='local-only'" class="diff-card-body" style="grid-template-columns:1fr 1fr 120px;">
                                    <div class="diff-card-col">
                                        <div class="diff-card-col-header">📱 本地</div>
                                        <div v-for="f of showFields(row)" :key="f" class="diff-row">
                                            <span class="diff-label">{{ fieldLabel(f) }}</span>
                                            <span class="diff-value">{{ formatField(row.local, f) }}</span>
                                        </div>
                                    </div>
                                    <div class="diff-card-col"><div class="diff-card-empty">— 服务器无此任务 —</div></div>
                                    <div class="diff-card-actions">
                                        <el-radio-group v-model="conflictChoices[row.id]._mode" size="small">
                                            <el-radio value="local">保留</el-radio>
                                            <el-radio value="drop">丢弃</el-radio>
                                        </el-radio-group>
                                    </div>
                                </div>
                                <!-- server-only: 单栏 + 选择 -->
                                <div v-else-if="row.type==='server-only'" class="diff-card-body" style="grid-template-columns:1fr 1fr 120px;">
                                    <div class="diff-card-col"><div class="diff-card-empty">— 本地无此任务 —</div></div>
                                    <div class="diff-card-col">
                                        <div class="diff-card-col-header">☁️ 服务器</div>
                                        <div v-for="f of showFields(row)" :key="f" class="diff-row">
                                            <span class="diff-label">{{ fieldLabel(f) }}</span>
                                            <span class="diff-value">{{ formatField(row.server, f) }}</span>
                                        </div>
                                    </div>
                                    <div class="diff-card-actions">
                                        <el-radio-group v-model="conflictChoices[row.id]._mode" size="small">
                                            <el-radio value="server">保留</el-radio>
                                            <el-radio value="drop">丢弃</el-radio>
                                        </el-radio-group>
                                    </div>
                                </div>
                                <!-- both: diff 视图 -->
                                <div v-else class="diff-card-body">
                                    <div class="diff-card-col">
                                        <div class="diff-card-col-header">📱 本地</div>
                                        <div v-for="f of row.diffs" :key="f">
                                            <div v-if="diffClass(row, f) === 'changed'" style="position:relative;">
                                                <div class="diff-row changed-old">
                                                    <span class="diff-label">{{ fieldLabel(f) }}</span>
                                                    <span class="diff-value">{{ formatField(row.local, f) }}</span>
                                                </div>
                                            </div>
                                            <div v-else-if="diffClass(row, f) === 'none'" class="diff-row">
                                                <span class="diff-label">{{ fieldLabel(f) }}</span>
                                                <span class="diff-value">{{ formatField(row.local, f) }}</span>
                                            </div>
                                        </div>
                                        <div v-if="!row.diffs.length" class="diff-card-empty">无差异</div>
                                    </div>
                                    <div class="diff-card-col">
                                        <div class="diff-card-col-header">☁️ 服务器</div>
                                        <div v-for="f of row.diffs" :key="f">
                                            <div v-if="diffClass(row, f) === 'changed'" style="position:relative;">
                                                <div class="diff-row changed-new">
                                                    <span class="diff-label">{{ fieldLabel(f) }}</span>
                                                    <span class="diff-value">{{ formatField(row.server, f) }}</span>
                                                </div>
                                            </div>
                                            <div v-else-if="diffClass(row, f) === 'none'" class="diff-row">
                                                <span class="diff-label">{{ fieldLabel(f) }}</span>
                                                <span class="diff-value">{{ formatField(row.server, f) }}</span>
                                            </div>
                                        </div>
                                        <div v-if="!row.diffs.length" class="diff-card-empty">无差异</div>
                                    </div>
                                    <div class="diff-card-actions">
                                        <el-radio-group v-model="conflictChoices[row.id]._mode" size="small">
                                            <el-radio value="local">取本地</el-radio>
                                            <el-radio value="server">取服务器</el-radio>
                                        </el-radio-group>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
                <div class="modal-actions" style="margin-top:0;flex-shrink:0;">
                    <button class="btn btn-secondary" @click="showManualResolve = false">取消</button>
                    <button class="btn btn-primary" @click="confirmManualResolve">确认合并</button>
                </div>
            </div>
        </div>

        <!-- Confirm Merge Summary Modal -->
        <div class="modal-mask" v-if="showConfirmSummary" @click.self="showConfirmSummary = false">
            <div class="modal-content" style="max-width:520px;height:auto;max-height:80vh;">
                <div class="modal-header">
                    <h3>📋 确认合并结果</h3>
                    <button class="modal-close" @click="showConfirmSummary = false">×</button>
                </div>
                <div class="modal-body" style="display:block;padding:16px 24px;overflow-y:auto;">
                    <p style="font-size:14px;line-height:2;">请确认以下合并结果，确认后将立即写入本地并同步服务器：</p>
                    <table class="summary-table">
                        <tbody>
                        <tr>
                            <td>保留本地整条</td>
                            <td class="summary-count" style="color:#1976d2;">{{ confirmSummary.localCount }}</td>
                            <td>条</td>
                        </tr>
                        <tr>
                            <td>保留服务器整条</td>
                            <td class="summary-count" style="color:#2e7d32;">{{ confirmSummary.serverCount }}</td>
                            <td>条</td>
                        </tr>
                        <tr>
                            <td>按字段合并</td>
                            <td class="summary-count" style="color:#fb8c00;">{{ confirmSummary.fieldCount }}</td>
                            <td>条</td>
                        </tr>
                        <tr>
                            <td>丢弃（仅本地/服务器）</td>
                            <td class="summary-count" style="color:#999;">{{ confirmSummary.dropCount }}</td>
                            <td>条</td>
                        </tr>
                        </tbody>
                    </table>
                    <p v-if="confirmSummary.fieldCount > 0" style="margin-top:10px;font-size:13px;color:#fb8c00;">
                        ⚠️ 按字段合并的任务将在各字段独立取对应版本，请确认字段选择无误。
                    </p>
                </div>
                <div class="modal-actions">
                    <button class="btn btn-secondary" @click="showConfirmSummary = false">返回修改</button>
                    <button class="btn btn-primary" @click="executeMerge">确认执行合并</button>
                </div>
            </div>
        </div>
    </div>
</template>

<!-- todo 样式已统一在 src/styles/todo.css（#todo-app 前缀全局样式） -->
