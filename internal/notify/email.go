// Package notify 事件订阅通知器。当前实现：EmailNotifier——
// 订阅 schedule 总线的 task.due 事件，向任务归属用户的邮箱投递到期提醒。
//
// 邮件发送：net/smtp + STARTTLS（587）。部署环境 465 出站 SSL 常被云厂商
// 干预不可用，统一走 587。SMTP 凭据经环境变量注入（EMAIL_USER/EMAIL_PASS），
// 不落 config 文件与仓库。
package notify

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/schedule"
)

// emailTimeout 单封邮件投递超时
const emailTimeout = 30 * time.Second

// EmailNotifier 邮件通知器：订阅 task.due → 发邮件给任务归属用户
type EmailNotifier struct {
	cfg config.EmailNotifyConfig
	db  *db.DB
}

// NewEmailNotifier 创建并订阅（enabled=false 时静默不订阅）。
// enabled 时校验必填项 host/user（SMTP 认证）；from 可选，
// 缺省回退 user（个人邮箱两者相同；仅企业邮局/代发服务场景才需要不同值）。
func NewEmailNotifier(cfg config.EmailNotifyConfig, database *db.DB, bus *schedule.EventBus) (*EmailNotifier, error) {
	n := &EmailNotifier{cfg: cfg, db: database}
	if !cfg.Enabled {
		return n, nil
	}
	if cfg.Host == "" || cfg.User == "" {
		return nil, fmt.Errorf("notify.email: host and user (SMTP auth) are required when enabled")
	}
	bus.Subscribe(schedule.TopicTaskDue, n.onTaskDue)
	bus.Subscribe(schedule.TopicTaskReminder, n.onTaskReminder)
	bus.Subscribe(schedule.TopicUserNotify, n.onUserNotify)
	slog.Info("email notifier subscribed", "host", cfg.Host, "from", n.from())
	return n, nil
}

// from 系统发件邮箱：配置显式指定，缺省回退认证用户名（个人邮箱场景）
func (n *EmailNotifier) from() string {
	if n.cfg.From != "" {
		return n.cfg.From
	}
	return n.cfg.User
}

// onTaskDue task.due 事件处理：查用户邮箱 → 组装正文 → 投递。
// 无邮箱/投递失败只记日志（提醒尽力而为，不影响总线与其他订阅者）。
func (n *EmailNotifier) onTaskDue(ctx context.Context, evt schedule.Event) {
	ev, ok := evt.Payload.(schedule.TaskDueEvent)
	if !ok {
		return
	}
	to, err := n.db.GetUserEmail(ctx, ev.UserID)
	if err != nil {
		slog.Error("email notify: get user email failed", "user", ev.UserID, "err", err)
		return
	}
	if to == "" {
		slog.Info("email notify: user has no email, skip", "user", ev.UserID, "task", ev.Task.ID)
		return
	}

	subject, body := dueMailContent(ev)
	if err := n.send(ctx, to, subject, body); err != nil {
		slog.Error("email notify: send failed", "to", to, "task", ev.Task.ID, "err", err)
		return
	}
	slog.Info("email notify: sent", "to", to, "task", string(ev.Task.ID), "overdue", ev.OverdueDays)
}

// onUserNotify 用户自定义通知事件处理（AI notify_user 工具发布）：
// 按事件里的 userID 查邮箱投递，主题/正文直接采用事件内容。
func (n *EmailNotifier) onUserNotify(ctx context.Context, evt schedule.Event) {
	ev, ok := evt.Payload.(schedule.UserNotifyEvent)
	if !ok {
		return
	}
	to, err := n.db.GetUserEmail(ctx, ev.UserID)
	if err != nil {
		slog.Error("email notify: get user email failed", "user", ev.UserID, "err", err)
		return
	}
	if to == "" {
		slog.Info("email notify: user has no email, skip", "user", ev.UserID, "subject", ev.Subject)
		return
	}
	if err := n.send(ctx, to, ev.Subject, ev.Body); err != nil {
		slog.Error("email notify: send failed", "to", to, "subject", ev.Subject, "err", err)
		return
	}
	slog.Info("email notify: sent (user.notify)", "to", to, "subject", ev.Subject)
}

// onTaskReminder 精确时间提醒事件处理：scheduled_time/due_time 到点触发
func (n *EmailNotifier) onTaskReminder(ctx context.Context, evt schedule.Event) {
	ev, ok := evt.Payload.(schedule.TaskReminderEvent)
	if !ok {
		return
	}
	to, err := n.db.GetUserEmail(ctx, ev.UserID)
	if err != nil {
		slog.Error("email notify: get user email failed", "user", ev.UserID, "err", err)
		return
	}
	if to == "" {
		return
	}
	subject, body := reminderMailContent(ev)
	if err := n.send(ctx, to, subject, body); err != nil {
		slog.Error("email notify: send failed", "to", to, "kind", ev.Kind, "err", err)
		return
	}
	slog.Info("email notify: sent (task.reminder)", "to", to, "kind", ev.Kind, "task", string(ev.Task.ID))
}

// reminderMailContent 组装精确时间提醒邮件：kind= start（计划开始）| due（截止）
func reminderMailContent(ev schedule.TaskReminderEvent) (subject, body string) {
	var head, action string
	if ev.Kind == "start" {
		head, action = "计划开始", "计划开始时间已到"
	} else {
		head, action = "即将截止", "截止时间已到"
	}
	subject = fmt.Sprintf("[Workbench] %s提醒：%s（%s）", head, ev.Task.Title, ev.At)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你的任务「%s」%s（%s）。\n\n", ev.Task.Title, action, ev.At))
	b.WriteString(fmt.Sprintf("计划日期：%s　截止日期：%s\n", ev.Task.Scheduled, ev.Task.Due))
	b.WriteString(fmt.Sprintf("状态：%s　优先级：%s　进度：%d%%\n", ev.Task.Status, ev.Task.Priority, ev.Task.Progress))
	b.WriteString("\n—— Workbench 定时提醒")
	return subject, b.String()
}

// dueMailContent 组装到期提醒的主题与正文（纯文本）
func dueMailContent(ev schedule.TaskDueEvent) (subject, body string) {
	var head string
	if ev.OverdueDays == 0 {
		head = "今天截止"
	} else {
		head = fmt.Sprintf("已逾期 %d 天", ev.OverdueDays)
	}
	subject = fmt.Sprintf("[Workbench] 任务提醒：%s（%s）", ev.Task.Title, head)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("你的任务「%s」%s。\n\n", ev.Task.Title, head))
	b.WriteString(fmt.Sprintf("截止日期：%s\n", ev.DueDate))
	b.WriteString(fmt.Sprintf("状态：%s　优先级：%s　进度：%d%%\n", ev.Task.Status, ev.Task.Priority, ev.Task.Progress))
	if ev.Task.Content != "" {
		content := ev.Task.Content
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		b.WriteString(fmt.Sprintf("\n备注：\n%s\n", content))
	}
	b.WriteString("\n—— Workbench 自动提醒，完成任务后不再提醒")
	return subject, b.String()
}

// send 经 SMTP 587 STARTTLS 投递（USER/PASS 认证，环境变量 EMAIL_PASS）
func (n *EmailNotifier) send(ctx context.Context, to, subject, body string) error {
	pass := emailPass()
	if pass == "" {
		return fmt.Errorf("EMAIL_PASS not set")
	}
	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)

	cctx, cancel := context.WithTimeout(ctx, emailTimeout)
	defer cancel()

	conn, err := smtpDial(cctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err = conn.Hello("workbench"); err != nil {
		return err
	}
	// STARTTLS 升级（不支持则拒绝投递，避免明文发送凭据）
	if ok, _ := conn.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp server does not support STARTTLS")
	}
	if err = conn.StartTLS(tlsConfig(n.cfg.Host)); err != nil {
		return err
	}
	auth := smtp.PlainAuth("", n.cfg.User, pass, n.cfg.Host)
	if err = conn.Auth(auth); err != nil {
		return err
	}
	if err = conn.Mail(n.from()); err != nil {
		return err
	}
	if err = conn.Rcpt(to); err != nil {
		return err
	}
	w, err := conn.Data()
	if err != nil {
		return err
	}
	msg := buildMail(n.from(), n.fromName(), to, subject, body)
	if _, err = w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

// fromName 发件人显示名（配置 from_name，缺省 "Workbench"；含非 ASCII 时 base64 编码）
func (n *EmailNotifier) fromName() string {
	if n.cfg.FromName != "" {
		return n.cfg.FromName
	}
	return "Workbench"
}

// buildMail 组装 RFC 5322 报文（UTF-8，base64 编码正文/主题/含非 ASCII 的显示名）
func buildMail(from, fromName, to, subject, body string) string {
	const layout = "Mon, 02 Jan 2006 15:04:05 -0700"
	var b strings.Builder
	b.WriteString(fmt.Sprintf("From: %s <%s>\r\n", encodeDisplayName(fromName), from))
	b.WriteString(fmt.Sprintf("To: <%s>\r\n", to))
	b.WriteString("Subject: " + encodeWord(subject) + "\r\n")
	b.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(layout)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(b64(body))
	return b.String()
}

// encodeWord RFC 2047 B 编码 encoded-word：按 ~40 字节原文切段（不切断多字节
// 字符），每段独立 encoded-word，段间以 CRLF+空格续行折叠。
// 关键：encoded-word 内部不允许换行、必须以 ?= 闭合——此前直接复用 b64()
// （每 76 字符折行且尾随换行）导致 Subject 编码词被截断，收件方无法解码原样显示。
func encodeWord(s string) string {
	src := []byte(s)
	const chunk = 40 // 40 字节 → ~54 base64 字符 + 标记 ≈ 64 < 75（RFC 2047 单行上限）
	var parts []string
	for len(src) > 0 {
		n := chunk
		if n > len(src) {
			n = len(src)
		}
		// 回退到 UTF-8 字符边界，避免切断多字节字符
		for n < len(src) && !runeStart(src[n]) {
			n--
		}
		parts = append(parts, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString(src[:n])+"?=")
		src = src[n:]
	}
	return strings.Join(parts, "\r\n ")
}

// encodeDisplayName 显示名编码：纯 ASCII 原样，否则 RFC 2047 B 编码（支持中文显示名）
func encodeDisplayName(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] > 126 || name[i] < 32 {
			return encodeWord(name)
		}
	}
	return name
}
