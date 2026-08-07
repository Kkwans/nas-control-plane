package terminal

type MessageType string

const (
	MessageStart   MessageType = "start"
	MessageStarted MessageType = "started"
	MessageInput   MessageType = "input"
	MessageOutput  MessageType = "output"
	MessageResize  MessageType = "resize"
	MessageClose   MessageType = "close"
	MessageClosed  MessageType = "closed"
)

// Message 是 Server 与 Agent 之间的受控终端流消息，不承载用户给出的命令、Shell、路径或环境变量。
type Message struct {
	Type         MessageType
	Target       Target
	ContainerID  string
	Rows         uint16
	Cols         uint16
	Data         []byte
	SessionID    string
	Shell        string
	Enhancement  string
	Reason       string
	Capabilities SessionCapabilities
}
