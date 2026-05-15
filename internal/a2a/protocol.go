package a2a

// AgentCard is the discovery document that describes an A2A agent.
type AgentCard struct {
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	URL          string       `json:"url"` // base URL for agent API
	Version      string       `json:"version"`
	Capabilities Capabilities `json:"capabilities"`
	Skills       []AgentSkill `json:"skills"`
}

type Capabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"push_notifications"`
}

type AgentSkill struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Task represents a unit of work in A2A.
type Task struct {
	ID     string     `json:"id"`
	State  TaskState  `json:"state"`
	Input  TaskInput  `json:"input,omitempty"`
	Output TaskOutput `json:"output,omitempty"`
}

type TaskState string

const (
	TaskStatePending    TaskState = "pending"
	TaskStateProcessing TaskState = "processing"
	TaskStateCompleted  TaskState = "completed"
	TaskStateFailed     TaskState = "failed"
)

type TaskInput struct {
	Message string `json:"message"`
	Context string `json:"context,omitempty"`
}

type TaskOutput struct {
	Text      string     `json:"text"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

type Artifact struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64 for binary, text for text
}
