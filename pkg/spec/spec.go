package spec

import "time"

type JSONSchema struct {
	Title                string                 `json:"title,omitempty"`
	Type                 string                 `json:"type,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Format               string                 `json:"format,omitempty"`
	Properties           map[string]*JSONSchema `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Items                *JSONSchema            `json:"items,omitempty"`
	Enum                 []any                  `json:"enum,omitempty"`
	Default              any                    `json:"default,omitempty"`
	Examples             []any                  `json:"examples,omitempty"`
	AdditionalProperties any                    `json:"additionalProperties,omitempty"`
}

type ToolSpec struct {
	ID           string         `json:"id"`
	Module       string         `json:"module"`
	Tool         string         `json:"tool"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary,omitempty"`
	Description  string         `json:"description"`
	InputSchema  *JSONSchema    `json:"input_schema"`
	OutputSchema *JSONSchema    `json:"output_schema,omitempty"`
	Downstream   DownstreamSpec `json:"downstream"`
	Hooks        HookChain      `json:"hooks,omitempty"`
	Authz        AuthzPolicy    `json:"authz_policy,omitempty"`
	Behavior     BehaviorSpec   `json:"behavior,omitempty"`
	Examples     []ToolExample  `json:"examples,omitempty"`
	ErrorCatalog []ToolError    `json:"error_catalog,omitempty"`
	Notes        []string       `json:"notes,omitempty"`
	Revision     int64          `json:"revision"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type DownstreamSpec struct {
	Type string      `json:"type"`
	HTTP *HTTPAction `json:"http,omitempty"`
	MCP  *MCPAction  `json:"mcp,omitempty"`
}

type HTTPAction struct {
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	TimeoutMillis int               `json:"timeout_ms,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Bindings      []Binding         `json:"bindings,omitempty"`
}

type MCPAction struct {
	ServerID string    `json:"server_id"`
	ToolName string    `json:"tool_name"`
	Bindings []Binding `json:"bindings,omitempty"`
}

type Binding struct {
	Source    string `json:"source"`
	Key       string `json:"key,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
	Target    string `json:"target"`
	Name      string `json:"name"`
	Value     any    `json:"value,omitempty"`
}

type HookChain struct {
	Pre  []HookSpec `json:"pre,omitempty"`
	Post []HookSpec `json:"post,omitempty"`
}

type HookSpec struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Interactive bool           `json:"interactive,omitempty"`
	HardFail    bool           `json:"hard_fail,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}

type AuthzPolicy struct {
	RequiredScopes []string `json:"required_scopes,omitempty"`
	AllowedRoles   []string `json:"allowed_roles,omitempty"`
	AllowedGroups  []string `json:"allowed_groups,omitempty"`
	AllowedUsers   []string `json:"allowed_users,omitempty"`
}

type BehaviorSpec struct {
	SideEffects string `json:"side_effects,omitempty"`
	Idempotency string `json:"idempotency,omitempty"`
	Retryable   bool   `json:"retryable,omitempty"`
	LatencyHint string `json:"latency_hint,omitempty"`
}

type ToolExample struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
}

type ToolError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type SecretSpec struct {
	Name       string    `json:"name"`
	Ciphertext string    `json:"ciphertext"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type MCPServerSpec struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Transport           string    `json:"transport"`
	Endpoint            string    `json:"endpoint"`
	BearerSecretRef     string    `json:"bearer_secret_ref,omitempty"`
	InitializationToken string    `json:"initialization_token,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type MCPToolDiscoveryItem struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema *JSONSchema `json:"input_schema,omitempty"`
}

type MCPServerTestResponse struct {
	Status string                 `json:"status"`
	OK     bool                   `json:"ok"`
	Tools  []MCPToolDiscoveryItem `json:"tools,omitempty"`
	Error  *APIError              `json:"error,omitempty"`
}

type MCPImportPreviewItem struct {
	RemoteName  string      `json:"remote_name"`
	LocalModule string      `json:"local_module"`
	LocalTool   string      `json:"local_tool"`
	Conflict    bool        `json:"conflict"`
	Reason      string      `json:"reason,omitempty"`
	Description string      `json:"description,omitempty"`
	InputSchema *JSONSchema `json:"input_schema,omitempty"`
}

type MCPImportPreviewResponse struct {
	Status   string                 `json:"status"`
	ServerID string                 `json:"server_id"`
	Items    []MCPImportPreviewItem `json:"items"`
	Error    *APIError              `json:"error,omitempty"`
}

type MCPImportResultItem struct {
	RemoteName  string `json:"remote_name"`
	LocalModule string `json:"local_module"`
	LocalTool   string `json:"local_tool"`
	Reason      string `json:"reason,omitempty"`
}

type MCPImportResult struct {
	Status   string                `json:"status"`
	ServerID string                `json:"server_id"`
	Imported []MCPImportResultItem `json:"imported,omitempty"`
	Skipped  []MCPImportResultItem `json:"skipped,omitempty"`
	Errors   []MCPImportResultItem `json:"errors,omitempty"`
	Error    *APIError             `json:"error,omitempty"`
}

type MCPServerDeleteResponse struct {
	Status       string    `json:"status"`
	ServerID     string    `json:"server_id"`
	DeletedTools []string  `json:"deleted_tools,omitempty"`
	Error        *APIError `json:"error,omitempty"`
}

type InvocationStatus string

const (
	InvocationStatusPending         InvocationStatus = "pending"
	InvocationStatusWaitingForHuman InvocationStatus = "waiting_for_human"
	InvocationStatusRunning         InvocationStatus = "running"
	InvocationStatusSucceeded       InvocationStatus = "succeeded"
	InvocationStatusFailed          InvocationStatus = "failed"
	InvocationStatusRejected        InvocationStatus = "rejected"
	InvocationStatusExpired         InvocationStatus = "expired"
)

type InvocationSession struct {
	ID                  string            `json:"id"`
	ToolID              string            `json:"tool_id"`
	Module              string            `json:"module"`
	Tool                string            `json:"tool"`
	Subject             string            `json:"subject"`
	Input               map[string]any    `json:"input"`
	CurrentHookIndex    int               `json:"current_hook_index"`
	Status              InvocationStatus  `json:"status"`
	Interaction         *InteractionState `json:"interaction,omitempty"`
	SubmitTokenHash     string            `json:"submit_token_hash,omitempty"`
	InteractionResponse any               `json:"interaction_response,omitempty"`
	SubmittedBy         *InteractionActor `json:"submitted_by,omitempty"`
	SubmittedAt         time.Time         `json:"submitted_at,omitempty"`
	Result              any               `json:"result,omitempty"`
	PostHooks           []PostHookResult  `json:"post_hooks,omitempty"`
	Error               *APIError         `json:"error,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	ExpiresAt           time.Time         `json:"expires_at"`
}

type Challenge struct {
	InvocationID string         `json:"invocation_id"`
	HookID       string         `json:"hook_id"`
	HookType     string         `json:"hook_type"`
	Prompt       string         `json:"prompt"`
	Payload      map[string]any `json:"payload,omitempty"`
	ExpiresAt    time.Time      `json:"expires_at"`
}

type InteractionState struct {
	HookID         string              `json:"hook_id"`
	HookType       string              `json:"hook_type"`
	Kind           string              `json:"kind"`
	Title          string              `json:"title,omitempty"`
	Prompt         string              `json:"prompt"`
	Options        []InteractionOption `json:"options,omitempty"`
	ResponseSchema *JSONSchema         `json:"response_schema,omitempty"`
	ExpiresAt      time.Time           `json:"expires_at"`
}

type InteractionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type InteractionActor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type PostHookResult struct {
	HookID  string         `json:"hook_id"`
	Type    string         `json:"type"`
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

type Envelope struct {
	Status          string           `json:"status"`
	InvocationID    string           `json:"invocation_id,omitempty"`
	Challenge       *Challenge       `json:"challenge,omitempty"`
	Result          any              `json:"result,omitempty"`
	PostHooks       []PostHookResult `json:"post_hooks,omitempty"`
	Error           *APIError        `json:"error,omitempty"`
	ToolRevision    int64            `json:"tool_revision,omitempty"`
	RetryHint       string           `json:"retry_hint,omitempty"`
	MayRetry        *bool            `json:"may_retry,omitempty"`
	HILState        string           `json:"hil_state,omitempty"`
	NormalizedError *APIError        `json:"normalized_error,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ResumeRequest struct {
	Response map[string]any `json:"response"`
}

type SubmitRequest struct {
	SubmitToken string           `json:"submit_token"`
	Decision    string           `json:"decision"`
	Response    any              `json:"response,omitempty"`
	Actor       InteractionActor `json:"actor"`
}

type SubmitResponse struct {
	Status       string    `json:"status"`
	InvocationID string    `json:"invocation_id"`
	FinalStatus  string    `json:"final_status"`
	Result       any       `json:"result,omitempty"`
	Error        *APIError `json:"error,omitempty"`
}

type InvocationView struct {
	ID               string            `json:"id"`
	ToolID           string            `json:"tool_id"`
	Subject          string            `json:"subject"`
	Status           InvocationStatus  `json:"status"`
	CurrentHookIndex int               `json:"current_hook_index"`
	Interaction      *InteractionState `json:"interaction,omitempty"`
	SubmittedBy      *InteractionActor `json:"submitted_by,omitempty"`
	SubmittedAt      time.Time         `json:"submitted_at,omitempty"`
	Result           any               `json:"result,omitempty"`
	Error            *APIError         `json:"error,omitempty"`
	PostHooks        []PostHookResult  `json:"post_hooks,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
}

type ModuleHelpDoc struct {
	Module      string            `json:"module"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Examples    []string          `json:"examples,omitempty"`
	Tools       []ToolSummaryHelp `json:"tools"`
}

type ToolSummaryHelp struct {
	Tool        string `json:"tool"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Usage       string `json:"usage,omitempty"`
}

type ToolHelpDoc struct {
	ID          string       `json:"id"`
	Module      string       `json:"module"`
	Tool        string       `json:"tool"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Usage       []string     `json:"usage"`
	Params      []HelpParam  `json:"params"`
	Examples    []string     `json:"examples,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	InputSchema *JSONSchema  `json:"input_schema,omitempty"`
	Behavior    BehaviorSpec `json:"behavior,omitempty"`
	Errors      []ToolError  `json:"errors,omitempty"`
}

type HelpParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

type ToolSearchResult struct {
	ID          string       `json:"id"`
	Module      string       `json:"module"`
	Tool        string       `json:"tool"`
	Title       string       `json:"title,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	Description string       `json:"description,omitempty"`
	Signature   string       `json:"signature,omitempty"`
	Usage       string       `json:"usage,omitempty"`
	Behavior    BehaviorSpec `json:"behavior,omitempty"`
}

type RuntimeCatalogRequest struct {
	Mode          string   `json:"mode"`
	Query         string   `json:"query,omitempty"`
	Modules       []string `json:"modules,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	IncludePython bool     `json:"include_python,omitempty"`
}

type RuntimeCatalogResponse struct {
	Scope   string                  `json:"scope"`
	Items   []RuntimeModuleCard     `json:"items,omitempty"`
	Modules []RuntimeModuleManifest `json:"modules,omitempty"`
	Page    *RuntimePage            `json:"page,omitempty"`
}

type RuntimeInvokeRequest struct {
	Module string         `json:"module"`
	Tool   string         `json:"tool"`
	Input  map[string]any `json:"input,omitempty"`
}

type RuntimePage struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

type RuntimeModuleCard struct {
	Module         string   `json:"module"`
	Summary        string   `json:"summary,omitempty"`
	TopTools       []string `json:"top_tools,omitempty"`
	PTCRecommended bool     `json:"ptc_recommended"`
	MayNeedHuman   bool     `json:"may_need_human"`
}

type RuntimeModuleManifest struct {
	Module  string                `json:"module"`
	Summary string                `json:"summary,omitempty"`
	Tools   []RuntimeToolManifest `json:"tools"`
	Python  string                `json:"python,omitempty"`
}

type RuntimeToolManifest struct {
	Module          string                          `json:"module"`
	Tool            string                          `json:"tool"`
	Revision        int64                           `json:"revision,omitempty"`
	Summary         string                          `json:"summary,omitempty"`
	Signature       string                          `json:"signature,omitempty"`
	RequiredArgs    []string                        `json:"required_args,omitempty"`
	ArgConstraints  map[string]RuntimeArgConstraint `json:"arg_constraints,omitempty"`
	Returns         string                          `json:"returns,omitempty"`
	SideEffectLevel string                          `json:"side_effect_level,omitempty"`
	MayTriggerHuman bool                            `json:"may_trigger_human"`
	RetryHint       string                          `json:"retry_hint,omitempty"`
	Example         string                          `json:"example,omitempty"`
	PTCCallable     bool                            `json:"ptc_callable"`
}

type RuntimeArgConstraint struct {
	Type        string   `json:"type,omitempty"`
	Format      string   `json:"format,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type FleetPendingClaim struct {
	PairingID    string    `json:"pairing_id"`
	DeviceID     string    `json:"device_id"`
	DisplayName  string    `json:"display_name,omitempty"`
	PublicKey    string    `json:"public_key,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	DeviceFamily string    `json:"device_family,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientMode   string    `json:"client_mode,omitempty"`
	Role         string    `json:"role,omitempty"`
	RemoteIP     string    `json:"remote_ip,omitempty"`
	Status       string    `json:"status"`
	RequestedAt  time.Time `json:"requested_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FleetOwnedDevice struct {
	UserID       string    `json:"user_id"`
	DeviceID     string    `json:"device_id"`
	DisplayName  string    `json:"display_name,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	DeviceFamily string    `json:"device_family,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientMode   string    `json:"client_mode,omitempty"`
	Role         string    `json:"role,omitempty"`
	RemoteIP     string    `json:"remote_ip,omitempty"`
	TokenState   string    `json:"token_state,omitempty"`
	ApprovedAt   time.Time `json:"approved_at"`
	LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FleetOwnedNode struct {
	UserID        string          `json:"user_id"`
	DeviceID      string          `json:"device_id"`
	NodeID        string          `json:"node_id"`
	BackendNodeID string          `json:"backend_node_id,omitempty"`
	DisplayName   string          `json:"display_name,omitempty"`
	Platform      string          `json:"platform,omitempty"`
	Version       string          `json:"version,omitempty"`
	CoreVersion   string          `json:"core_version,omitempty"`
	UIVersion     string          `json:"ui_version,omitempty"`
	ClientID      string          `json:"client_id,omitempty"`
	ClientMode    string          `json:"client_mode,omitempty"`
	RemoteIP      string          `json:"remote_ip,omitempty"`
	DeviceFamily  string          `json:"device_family,omitempty"`
	ModelID       string          `json:"model_identifier,omitempty"`
	PathEnv       string          `json:"path_env,omitempty"`
	Caps          []string        `json:"caps,omitempty"`
	Commands      []string        `json:"commands,omitempty"`
	Permissions   map[string]bool `json:"permissions,omitempty"`
	Status        string          `json:"status"`
	Paired        bool            `json:"paired"`
	Connected     bool            `json:"connected"`
	ConnectedAt   time.Time       `json:"connected_at,omitempty"`
	ApprovedAt    time.Time       `json:"approved_at,omitempty"`
	LastSeenAt    time.Time       `json:"last_seen_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type FleetInvokeRequest struct {
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
}

type FleetInvokeResponse struct {
	NodeID      string `json:"node_id"`
	Command     string `json:"command"`
	OK          bool   `json:"ok"`
	Payload     any    `json:"payload,omitempty"`
	PayloadJSON string `json:"payload_json,omitempty"`
}

type FleetRunRequest struct {
	Command []string          `json:"command"`
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type FleetRunResponse struct {
	NodeID   string         `json:"node_id"`
	Accepted bool           `json:"accepted"`
	Result   map[string]any `json:"result,omitempty"`
}

type FleetNodeAuthState struct {
	DeviceID        string          `json:"device_id"`
	NodeID          string          `json:"node_id"`
	DisplayName     string          `json:"display_name,omitempty"`
	PublicKey       string          `json:"public_key,omitempty"`
	Role            string          `json:"role,omitempty"`
	Scopes          []string        `json:"scopes,omitempty"`
	ClientID        string          `json:"client_id,omitempty"`
	ClientMode      string          `json:"client_mode,omitempty"`
	Platform        string          `json:"platform,omitempty"`
	DeviceFamily    string          `json:"device_family,omitempty"`
	ModelID         string          `json:"model_identifier,omitempty"`
	Version         string          `json:"version,omitempty"`
	PathEnv         string          `json:"path_env,omitempty"`
	Commands        []string        `json:"commands,omitempty"`
	Caps            []string        `json:"caps,omitempty"`
	Permissions     map[string]bool `json:"permissions,omitempty"`
	BootstrapMethod string          `json:"bootstrap_method,omitempty"`
	TokenHash       string          `json:"token_hash,omitempty"`
	TokenIssuedAt   time.Time       `json:"token_issued_at,omitempty"`
	LastSeenAt      time.Time       `json:"last_seen_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
