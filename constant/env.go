package constant

var StreamingTimeout int
var DifyDebug bool
var MaxFileDownloadMB int

// StreamingMaxBufferSize is the byte-sized SSE event limit configured
// through STREAMING_MAX_BUFFER_SIZE. A non-positive value means unset.
var StreamingMaxBufferSize int

// StreamScannerMaxBufferMB is the legacy compatibility SSE event limit configured
// through STREAM_SCANNER_MAX_BUFFER_MB. A non-positive value means unset.
var StreamScannerMaxBufferMB int
var ForceStreamOption bool
var CountToken bool
var GetMediaToken bool
var GetMediaTokenNotStream bool
var UpdateTask bool
var MaxRequestBodyMB int
var AnonymousRequestBodyLimitKB int
var AzureDefaultAPIVersion string
var NotifyLimitCount int
var NotificationLimitDurationMinute int
var GenerateDefaultToken bool
var ErrorLogEnabled bool
var TaskQueryLimit int
var TaskTimeoutMinutes int

// RelayUpstreamHTTPMode is the optional process-wide upstream HTTP mode
// configured through RELAY_UPSTREAM_HTTP_MODE. An empty value means unset.
var RelayUpstreamHTTPMode string

// RelayHTTP2ConnectionPoolSize and RelayHTTP1BodyThresholdKiB are optional
// process-wide defaults. A non-positive value means unset.
var RelayHTTP2ConnectionPoolSize int
var RelayHTTP1BodyThresholdKiB int

const (
	DefaultSSEMaxEventSizeMB = 16
	MinSSEMaxEventSizeMB     = 1
	MaxSSEMaxEventSizeMB     = 128

	UpstreamHTTPModeAuto   = "auto"
	UpstreamHTTPModeHTTP1  = "http1"
	UpstreamHTTPModeHybrid = "hybrid"

	DefaultHTTP2ConnectionPoolSize = 1
	MinHTTP2ConnectionPoolSize     = 1
	MaxHTTP2ConnectionPoolSize     = 64
	DefaultHTTP1BodyThresholdKiB   = 256
	MinHTTP1BodyThresholdKiB       = 64
	MaxHTTP1BodyThresholdKiB       = 65536
)

// temporary variable for sora patch, will be removed in future
var TaskPricePatches []string

// TrustedRedirectDomains is a list of trusted domains for redirect URL validation.
// Domains support subdomain matching (e.g., "example.com" matches "sub.example.com").
var TrustedRedirectDomains []string
