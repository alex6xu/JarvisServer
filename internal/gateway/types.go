package gateway

// ChatRequest is the body of POST /v1/agent/chat.
type ChatRequest struct {
	Message     string `json:"message"`
	SessionID   string `json:"session_id,omitempty"`
	Model       string `json:"model,omitempty"`
	Stream      bool   `json:"stream,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Mode        string `json:"mode,omitempty"` // "chat" (personal Jarvis) or "coder" (coding Jarvis)
	Pinned      bool   `json:"pinned,omitempty"`
	AccountID   int    `json:"-"`
}

// ChatResponse is returned immediately; the client then opens the SSE stream.
type ChatResponse struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Model     string `json:"model,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
}

// ToolStep matches web/src/lib/sessionPersist.ts ToolStep.
type ToolStep struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Result string `json:"result"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"` // running | done | error
}

// StreamEvent matches web AgentStreamEvent (SSE payload).
type StreamEvent struct {
	Type      string     `json:"type,omitempty"`
	Content   string     `json:"content,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	Model     string     `json:"model,omitempty"`
	Step      *ToolStep  `json:"step,omitempty"`
	ToolSteps []ToolStep `json:"tool_steps,omitempty"`
	Seq       int64      `json:"seq,omitempty"`
	AttemptID string     `json:"attempt_id,omitempty"`
	Purpose   string     `json:"purpose,omitempty"`
	Pinned    bool       `json:"pinned,omitempty"`
	Timestamp string     `json:"timestamp,omitempty"`
}

// StoredEvent is one sequenced SSE payload kept for after_seq replay.
type StoredEvent struct {
	Seq     int64
	Payload StreamEvent
}

// ActiveRunInfo matches web ActiveRunInfo.
type ActiveRunInfo struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	LastSeq     int64  `json:"last_seq"`
	Model       string `json:"model,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	DeadlineAt  string `json:"deadline_at,omitempty"`
}

// RestoredMessage matches web RestoredSessionMessage.
type RestoredMessage struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Model     string     `json:"model,omitempty"`
	CreatedAt string     `json:"created_at,omitempty"`
	ToolSteps []ToolStep `json:"tool_steps,omitempty"`
}

// SessionMeta is the session object inside SessionDetailResponse.
type SessionMeta struct {
	ID              string `json:"id"`
	Title           string `json:"title,omitempty"`
	Type            string `json:"type"`
	Platform        string `json:"platform,omitempty"`
	MessageCount    int    `json:"message_count,omitempty"`
	Model           string `json:"model,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Preview         string `json:"preview,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	ActiveRunStatus string `json:"active_run_status,omitempty"`
	ParentSession   string `json:"parent_session,omitempty"`
	WorktreeBranch  string `json:"worktree_branch,omitempty"`
	BaseCommit      string `json:"base_commit,omitempty"`
}

type ForkSessionRequest struct {
	EntryID  string `json:"entry_id,omitempty"`
	Position string `json:"position,omitempty"` // at | before
}

type ForkSessionResponse struct {
	Session     SessionMeta `json:"session"`
	WorkspaceID string      `json:"workspace_id"`
}

type MergeSessionResponse struct {
	Message      string `json:"message"`
	ChangedFiles int    `json:"changed_files"`
}

// SessionDetailResponse matches web SessionRestorePayload.
type SessionDetailResponse struct {
	Session      SessionMeta       `json:"session"`
	Messages     []RestoredMessage `json:"messages"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`
	ActiveRun    *ActiveRunInfo    `json:"active_run,omitempty"`
	LastEventSeq int64             `json:"last_event_seq,omitempty"`
}

// SessionListResponse is GET /v1/agent/sessions.
type SessionListResponse struct {
	Sessions []SessionMeta `json:"sessions"`
}

// ActiveSessionResponse identifies the server-side session selected for a scope.
type ActiveSessionResponse struct {
	SessionID   string `json:"session_id"`
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// SetActiveSessionRequest is PUT /v1/agent/sessions/active.
type SetActiveSessionRequest struct {
	SessionID   string `json:"session_id"`
	Mode        string `json:"mode"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// Account is the stub auth user shape expected by web AuthContext.
type Account struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Quota     int    `json:"quota"`
	UsedQuota int    `json:"used_quota"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ErrorBody is a JSON error envelope.
type ErrorBody struct {
	Error string `json:"error"`
}
