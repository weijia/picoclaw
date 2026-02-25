package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"

	"gosrc.io/xmpp"
	"gosrc.io/xmpp/stanza"
)

// safePrintf 安全的printf函数，检查stdout是否有效
func safePrintf(format string, v ...interface{}) {
	var stdout syscall.Handle
	stdout, _ = syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if stdout != 0 {
		fmt.Printf(format, v...)
	}
}

// XMPPChannel XMPP通道结构
type XMPPChannel struct {
	*BaseChannel
	config          config.XMPPConfig
	client          *xmpp.Client
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	connected       bool
	authorizedUsers map[string]bool
}

// NewXMPPChannel 创建新的XMPP通道
func NewXMPPChannel(cfg config.XMPPConfig, bus *bus.MessageBus) (*XMPPChannel, error) {
	if cfg.Server == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("xmpp server, username, and password are required")
	}

	base := NewBaseChannel("xmpp", cfg, bus, cfg.AllowFrom)

	authorizedUsers := make(map[string]bool)
	for _, user := range cfg.AllowFrom {
		if user != "" {
			authorizedUsers[user] = true
		}
	}

	return &XMPPChannel{
		BaseChannel:     base,
		config:          cfg,
		connected:       false,
		authorizedUsers: authorizedUsers,
	}, nil
}

// Name 返回通道名称
func (c *XMPPChannel) Name() string {
	return "xmpp"
}

// Start 启动XMPP通道
func (c *XMPPChannel) Start(ctx context.Context) error {
	if c.IsRunning() {
		return fmt.Errorf("xmpp channel already running")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)

	logger.InfoCF("channels", "Connecting to XMPP server", map[string]any{
		"server": c.config.Server,
		"port":   c.config.Port,
		"user":   c.config.Username,
	})

	// 创建路由器
	router := xmpp.NewRouter()
	router.HandleFunc("message", c.handleMessage)

	// 配置XMPP客户端
	xmppConfig := xmpp.Config{
		TransportConfiguration: xmpp.TransportConfiguration{
			Address: fmt.Sprintf("%s:%d", c.config.Server, c.config.Port),
		},
		Jid:        c.config.Username,
		Credential: xmpp.Password(c.config.Password),
	}

	// 创建XMPP客户端
	client, err := xmpp.NewClient(&xmppConfig, router, func(err error) {
		logger.ErrorCF("channels", "XMPP client error", map[string]any{
			"error":  err.Error(),
			"server": c.config.Server,
			"user":   c.config.Username,
		})
	})
	if err != nil {
		logger.ErrorCF("channels", "Failed to create XMPP client", map[string]any{
			"error":  err.Error(),
			"server": c.config.Server,
			"user":   c.config.Username,
		})
		return fmt.Errorf("failed to create xmpp client: %w", err)
	}

	// 连接到服务器
	err = client.Connect()
	if err != nil {
		logger.ErrorCF("channels", "Failed to connect to XMPP server", map[string]any{
			"error":  err.Error(),
			"server": c.config.Server,
			"user":   c.config.Username,
		})
		return fmt.Errorf("failed to connect to xmpp server: %w", err)
	}

	c.client = client
	c.connected = true
	c.setRunning(true)

	logger.InfoCF("channels", "XMPP channel started successfully", map[string]any{
		"server": c.config.Server,
		"user":   c.config.Username,
	})

	return nil
}

// Stop 停止XMPP通道
func (c *XMPPChannel) Stop(ctx context.Context) error {
	if !c.IsRunning() {
		return nil
	}

	logger.InfoC("channels", "Stopping XMPP channel")

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		logger.DebugC("channels", "Canceled context")
	}

	if c.client != nil {
		logger.DebugC("channels", "Disconnecting XMPP client")
		c.client.Disconnect()
		logger.DebugC("channels", "XMPP client disconnected")
	}

	c.connected = false
	c.setRunning(false)

	logger.InfoC("channels", "XMPP channel stopped")

	return nil
}

// Send 发送消息
func (c *XMPPChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("xmpp channel not connected")
	}

	// 创建XMPP消息
	xmppMsg := stanza.Message{
		Attrs: stanza.Attrs{
			To: msg.ChatID,
		},
		Body: msg.Content,
	}

	// 发送消息
	err := c.client.Send(xmppMsg)
	if err != nil {
		logger.ErrorCF("channels", "Failed to send XMPP message", map[string]any{
			"error": err.Error(),
			"to":    msg.ChatID,
		})
		return fmt.Errorf("failed to send xmpp message: %w", err)
	}

	logger.DebugCF("channels", "XMPP message sent successfully", map[string]any{
		"to":      msg.ChatID,
		"content": msg.Content,
	})

	return nil
}

// IsRunning 检查通道是否运行
func (c *XMPPChannel) IsRunning() bool {
	return c.running
}

// IsAllowed 检查发送者是否被允许
func (c *XMPPChannel) IsAllowed(senderID string) bool {
	return c.BaseChannel.IsAllowed(senderID)
}

// handleMessage 处理接收到的消息
func (c *XMPPChannel) handleMessage(s xmpp.Sender, p stanza.Packet) {
	msg, ok := p.(stanza.Message)
	if !ok {
		return
	}

	if msg.Body == "" {
		return
	}

	sender := msg.From
	logger.InfoCF("channels", "Received XMPP message", map[string]any{
		"sender":  sender,
		"content": msg.Body,
		"server":  c.config.Server,
	})

	// 检查用户是否被允许
	if !c.isAuthorized(sender) {
		logger.WarnCF("channels", "Unauthorized user tried to send message", map[string]any{
			"sender": sender,
			"server": c.config.Server,
		})
		// 发送错误消息
		errorMsg := stanza.Message{
			Attrs: stanza.Attrs{
				To: sender,
			},
			Body: "Error: You are not authorized to send commands",
		}
		if err := c.client.Send(errorMsg); err != nil {
			logger.ErrorCF("channels", "Failed to send error message", map[string]any{
				"error": err.Error(),
				"to":    sender,
			})
		}
		return
	}

	// 创建入站消息
	inboundMsg := bus.InboundMessage{
		Channel:  c.Name(),
		ChatID:   sender,
		SenderID: sender,
		Content:  msg.Body,
		Metadata: map[string]string{
			"server": c.config.Server,
			"user":   c.config.Username,
		},
	}

	// 发送到消息总线
	c.bus.PublishInbound(inboundMsg)
	logger.DebugCF("channels", "Message sent to bus", map[string]any{
		"sender":  sender,
		"content": msg.Body,
	})
}

// isAuthorized 检查用户是否有权限
func (c *XMPPChannel) isAuthorized(user string) bool {
	// 如果没有设置授权用户，则允许所有用户
	if len(c.authorizedUsers) == 0 {
		logger.DebugCF("channels", "No authorized users set, allowing all", map[string]any{
			"user": user,
		})
		return true
	}

	// 精确匹配
	if c.authorizedUsers[user] {
		logger.DebugCF("channels", "User authorized (exact match)", map[string]any{
			"user": user,
		})
		return true
	}

	// 提取Bare JID（去除资源部分）进行匹配
	bareJID := extractBareJID(user)
	if bareJID != "" && c.authorizedUsers[bareJID] {
		logger.DebugCF("channels", "User authorized (bare JID match)", map[string]any{
			"user":    user,
			"bare_jid": bareJID,
		})
		return true
	}

	// 检查是否有通配符
	for authorizedUser := range c.authorizedUsers {
		if authorizedUser == "*" {
			logger.DebugCF("channels", "User authorized (wildcard match)", map[string]any{
				"user": user,
			})
			return true
		}
	}

	logger.DebugCF("channels", "User not authorized", map[string]any{
		"user": user,
	})
	return false
}

// extractBareJID 从JID中提取Bare JID（去除资源部分）
func extractBareJID(jid string) string {
	if slashIndex := strings.Index(jid, "/"); slashIndex > 0 {
		return jid[:slashIndex]
	}
	return jid
}
