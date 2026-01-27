package models

import (
	"encoding/base64"

	"github.com/rohanthewiz/serr"
	"github.com/vmihailenco/msgpack/v5"
)

// MsgPackBodyRequest represents the JSON request format when msgpack encoding is used
// for the body field. All other fields remain as regular JSON values.
//
// Design rationale: Using a hybrid JSON/msgpack approach where only the body field
// is msgpack-encoded offers several benefits:
// - Reduces bandwidth for large note content (msgpack is ~30% smaller than JSON strings)
// - Keeps metadata human-readable for easier debugging and logging
// - Maintains backwards compatibility with standard JSON-only clients
// - Client signals msgpack mode via X-Body-Encoding: msgpack header
type MsgPackBodyRequest struct {
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	BodyEncoded  string  `json:"body_encoded"` // Base64-encoded msgpack bytes
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	EncryptionIV *string `json:"encryption_iv,omitempty"`
}

// MsgPackBodyResponse represents the JSON response format when msgpack encoding is used.
// The body_encoded field contains Base64-encoded msgpack bytes instead of plain body.
type MsgPackBodyResponse struct {
	ID           int64   `json:"id"`
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	BodyEncoded  string  `json:"body_encoded"` // Base64-encoded msgpack bytes
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	EncryptionIV *string `json:"encryption_iv,omitempty"`
	CreatedBy    *string `json:"created_by,omitempty"`
	UpdatedBy    *string `json:"updated_by,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	AuthoredAt   *string `json:"authored_at,omitempty"`
	SyncedAt     *string `json:"synced_at,omitempty"`
	DeletedAt    *string `json:"deleted_at,omitempty"`
}

// EncodeMsgPackBody encodes a string body to Base64-encoded msgpack bytes.
// This is used for responses when client requests msgpack encoding via header.
//
// Encoding pipeline: string -> msgpack bytes -> Base64 string
// Returns empty string for nil or empty input to maintain backwards compatibility.
func EncodeMsgPackBody(body *string) (string, error) {
	if body == nil || *body == "" {
		return "", nil
	}

	// Encode string to msgpack bytes
	// msgpack efficiently encodes strings with length prefix
	msgpackBytes, err := msgpack.Marshal(*body)
	if err != nil {
		return "", serr.Wrap(err, "failed to msgpack encode body")
	}

	// Encode to Base64 for safe JSON transport
	// Using standard encoding (not URL-safe) since this is in JSON body
	return base64.StdEncoding.EncodeToString(msgpackBytes), nil
}

// DecodeMsgPackBody decodes a Base64-encoded msgpack string to plain text.
// This is used for requests when client sends msgpack-encoded body.
//
// Decoding pipeline: Base64 string -> msgpack bytes -> string
// Returns nil for empty input to maintain backwards compatibility.
func DecodeMsgPackBody(encoded string) (*string, error) {
	if encoded == "" {
		return nil, nil
	}

	// Decode Base64 to bytes
	msgpackBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, serr.Wrap(err, "failed to decode base64 body")
	}

	// Decode msgpack to string
	var body string
	if err := msgpack.Unmarshal(msgpackBytes, &body); err != nil {
		return nil, serr.Wrap(err, "failed to unmarshal msgpack body")
	}

	return &body, nil
}

// ToMsgPackResponse converts a NoteOutput to MsgPackBodyResponse.
// The body field is encoded to Base64 msgpack format while all other
// fields are copied as-is for standard JSON serialization.
func (n *NoteOutput) ToMsgPackResponse() (*MsgPackBodyResponse, error) {
	encodedBody, err := EncodeMsgPackBody(n.Body)
	if err != nil {
		return nil, err
	}

	return &MsgPackBodyResponse{
		ID:           n.ID,
		GUID:         n.GUID,
		Title:        n.Title,
		Description:  n.Description,
		BodyEncoded:  encodedBody,
		Tags:         n.Tags,
		IsPrivate:    n.IsPrivate,
		EncryptionIV: n.EncryptionIV,
		CreatedBy:    n.CreatedBy,
		UpdatedBy:    n.UpdatedBy,
		CreatedAt:    n.CreatedAt,
		UpdatedAt:    n.UpdatedAt,
		AuthoredAt:   n.AuthoredAt,
		SyncedAt:     n.SyncedAt,
		DeletedAt:    n.DeletedAt,
	}, nil
}

// ToNoteInput converts a MsgPackBodyRequest to NoteInput.
// The body_encoded field is decoded from Base64 msgpack format to plain string.
func (r *MsgPackBodyRequest) ToNoteInput() (*NoteInput, error) {
	body, err := DecodeMsgPackBody(r.BodyEncoded)
	if err != nil {
		return nil, err
	}

	return &NoteInput{
		GUID:         r.GUID,
		Title:        r.Title,
		Description:  r.Description,
		Body:         body,
		Tags:         r.Tags,
		IsPrivate:    r.IsPrivate,
		EncryptionIV: r.EncryptionIV,
	}, nil
}
