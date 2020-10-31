package batch

import "time"

type Request struct {
	Operation string   `json:"operation"`
	Transfers []string `json:"transfers"`
	Ref       struct {
		Name string `json:"name"`
	} `json:"ref"`
	Objects []struct {
		OID  string `json:"oid"`
		Size int    `json:"size"`
	} `json:"objects"`
}

type ErrorResponse struct {
	Message   string `json:"message"`
	DocURL    string `json:"documentation_url,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type Action struct {
	HRef      string            `json:"href"`
	Header    map[string]string `json:"header,omitempty"`
	ExpiresIn int               `json:"expires_in,omitempty"` // seconds
	ExpiresAt *RFC3339          `json:"expires_at,omitempty"`
}

type Actions struct {
	Download *Action  `json:"download,omitempty"`
	Upload   *Action  `json:"upload,omitempty"`
	Verify   *Actions `json:"verify,omitempty"`
}

type ObjectError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Object struct {
	OID           string       `json:"oid"`
	Size          int          `json:"size"`
	Authenticated bool         `json:"authenticated,omitempty"`
	Actions       *Actions     `json:"actions,omitempty"`
	Error         *ObjectError `json:"error,omitempty"`
}

type Response struct {
	Transfer string   `json:"transfer,omitempty"`
	Objects  []Object `json:"objects"`
}

// --

// RFC3339 JSON-encodes a time.Time as an RFC3339 string
// (as opposed to RFC3339Nano, which is default behavior)
type RFC3339 struct {
	T time.Time
}

// MarshalJSON implements json.Marshaler
func (t RFC3339) MarshalJSON() ([]byte, error) {
	return t.T.Truncate(time.Second).MarshalJSON()
}
