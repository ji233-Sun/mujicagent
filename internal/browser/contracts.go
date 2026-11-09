// Package browser defines the shared contract for browser UI and Codex tools.
// It contains no engine, persistence, or successful placeholder implementation.
// See README.md for authorization, serialization, and completion requirements.
package browser

import (
	"context"
	"errors"
	"time"
)

// DefaultTimeout bounds each call unless the caller has an earlier deadline.
const DefaultTimeout = 15 * time.Second

var (
	ErrInvalidArgument  = errors.New("browser: invalid argument")
	ErrNotFound         = errors.New("browser: page not found")
	ErrClosed           = errors.New("browser: page closed")
	ErrStale            = errors.New("browser: stale reference")
	ErrBusy             = errors.New("browser: page busy")
	ErrPermissionDenied = errors.New("browser: permission denied")
	ErrUnsupported      = errors.New("browser: unsupported capability")
	ErrSensitive        = errors.New("browser: sensitive data cannot be safely handled")
	ErrUnconfirmed      = errors.New("browser: target change not confirmed")
	ErrEngine           = errors.New("browser: engine failure")
)

// PageKey identifies one ephemeral page for a local model.Thread.ID, never a
// Codex ServerID. PageID is host-generated and is never reused after Close.
type PageKey struct {
	ThreadID string
	PageID   string
}

// PageRef adds the host's navigation generation (starting at 1).
// Generation 0 is invalid; every navigation start invalidates the old value.
type PageRef struct {
	PageKey
	Navigation uint64
}

// ElementRef belongs to exactly one successful Observe result. It is opaque;
// callers must not construct CSS selectors or engine handles from ID.
type ElementRef struct {
	PageRef
	ObservationID string
	ID            string
}

type State string

const (
	StateIdle   State = "idle"
	StateAgent  State = "agent"
	StateHuman  State = "human"
	StateClosed State = "closed"
)

// Permissions apply only to Agent requests. Observe needs no additional grant
// after explicit Resume. All optional permissions default to false.
type Permissions struct {
	Navigate bool
	Interact bool // Click, Fill, and Scroll.
	Capture  bool // Explicit consent to share the redacted visible viewport.
}

// Status is a detached snapshot. Busy also gates native human input while a
// previously dispatched operation drains during TakeOver or Close.
type Status struct {
	Page             PageRef
	State            State
	Busy             bool
	Loading          bool
	URL              string // Sanitized display URL; never use it for authorization.
	Title            string // Sanitized, untrusted page data.
	Permissions      Permissions
	LastError        error // Safe sentinel/wrapped sentinel, not raw engine/page text.
	CaptureSupported bool  // Native capture helper is configured for this page.
}

type Actor string

const (
	ActorAgent Actor = "agent"
	ActorHuman Actor = "human"
)

// Request is filled by the trusted host. Actor and ThreadID are never accepted
// from model-supplied arguments. Human requests require StateHuman.
type Request struct {
	Page  PageRef
	Actor Actor
}

type OpenRequest struct {
	Request
	URL string // Absolute http(s), without credentials; never log verbatim.
}

type ClickRequest struct {
	Request
	Element ElementRef
}

type FillRequest struct {
	Request
	Element ElementRef
	Text    string // Replace the whole value; sensitive fields are rejected.
}

// ScrollRequest moves the main viewport by signed CSS pixels, not device pixels.
// At least one finite delta must be nonzero. Nested scrolling is not implied.
type ScrollRequest struct {
	Request
	DeltaX float64
	DeltaY float64
}

type Dispatch string

const (
	DispatchUnknown Dispatch = "unknown"
	DispatchNone    Dispatch = "not_dispatched"
	DispatchSent    Dispatch = "dispatched"
)

type Completion string

const (
	CompletionUnconfirmed Completion = "unconfirmed"
	CompletionObserved    Completion = "observed"
)

// ActionResult is meaningful even with a non-nil error. Empty enum values must
// be treated as unknown/unconfirmed, never as success or safe-to-retry.
type ActionResult struct {
	Page       PageRef // Latest known generation, which may differ from Request.
	Dispatch   Dispatch
	Completion Completion
	Evidence   string // Bounded, sanitized observation; not a claim of business success.
}

type Element struct {
	Ref      ElementRef
	Role     string
	Name     string // Sanitized accessible name; no form value is returned.
	CanClick bool
	CanFill  bool
}

// Observation contains bounded, sanitized, untrusted visible-page data, not raw
// HTML, script, storage, headers, or an authorization decision.
type Observation struct {
	Page          PageRef
	ObservationID string
	URL           string
	Title         string
	Text          string
	Elements      []Element
	Truncated     bool
}

// Screenshot covers only this page's visible viewport after redaction.
// PNG bytes must not be logged or persisted by default. Dimensions are pixels.
type Screenshot struct {
	Page   PageRef
	PNG    []byte
	Width  int
	Height int
}

// Tools is shared by the UI host and Codex dispatcher. Calls serialize per page
// without an implicit queue. Each call observes cancellation and DefaultTimeout.
// Nil error on an action requires CompletionObserved and concrete Evidence.
type Tools interface {
	Open(context.Context, OpenRequest) (ActionResult, error)
	Observe(context.Context, Request) (Observation, error)
	Click(context.Context, ClickRequest) (ActionResult, error)
	Fill(context.Context, FillRequest) (ActionResult, error)
	Scroll(context.Context, ScrollRequest) (ActionResult, error)
	Capture(context.Context, Request) (Screenshot, error)
}

// Sessions is a host-only lifecycle interface; it is never exposed as model
// tools. Methods are safe to call concurrently with Tools and each other.
type Sessions interface {
	// Create makes a blank page in a fresh ephemeral session, initially Human.
	// If this thread already has a live page, return its unchanged status.
	Create(ctx context.Context, threadID string) (Status, error)
	Status(ctx context.Context, threadID string) (Status, error)
	// TakeOver revokes Agent access immediately, then drains in-flight work.
	TakeOver(context.Context, PageKey) (Status, error)
	// Resume is an explicit user action, valid only in Human and not Busy.
	Resume(context.Context, PageKey, Permissions) (Status, error)
	// Close revokes all access and destroys the page/session; it is idempotent
	// for this PageKey and cannot close a replacement page for the same thread.
	Close(context.Context, PageKey) (Status, error)
}
