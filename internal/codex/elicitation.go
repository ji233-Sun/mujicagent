package codex

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

type ElicitationOption struct {
	Value string `json:"const"`
	Title string `json:"title"`
}

type ElicitationField struct {
	Type        string              `json:"type"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Enum        []string            `json:"enum"`
	EnumNames   []string            `json:"enumNames"`
	OneOf       []ElicitationOption `json:"oneOf"`
	AnyOf       []ElicitationOption `json:"anyOf"`
	Items       *ElicitationField   `json:"items"`
	Minimum     *float64            `json:"minimum"`
	Maximum     *float64            `json:"maximum"`
	MinLength   *int                `json:"minLength"`
	MaxLength   *int                `json:"maxLength"`
	MinItems    *int                `json:"minItems"`
	MaxItems    *int                `json:"maxItems"`
	Format      string              `json:"format"`
}

func (f ElicitationField) Options() []ElicitationOption {
	if f.Type == "array" && f.Items != nil {
		return f.Items.Options()
	}
	if f.OneOf != nil {
		return f.OneOf
	}
	if f.AnyOf != nil {
		return f.AnyOf
	}
	options := make([]ElicitationOption, 0, len(f.Enum))
	for i, value := range f.Enum {
		title := value
		if i < len(f.EnumNames) {
			title = f.EnumNames[i]
		}
		options = append(options, ElicitationOption{Value: value, Title: title})
	}
	return options
}

type ElicitationForm struct {
	Type       string                      `json:"type"`
	Properties map[string]ElicitationField `json:"properties"`
	Required   []string                    `json:"required"`
}

func (f ElicitationForm) Keys() []string {
	keys := make([]string, 0, len(f.Properties))
	for key := range f.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ElicitationForm accepts standard MCP forms only. Extended modes require
// their own renderer and must never fall through to ordinary approval.
func (r *Request) ElicitationForm() (ElicitationForm, error) {
	var form ElicitationForm
	if r.Mode != "form" {
		return form, fmt.Errorf("unsupported elicitation mode: %s", r.Mode)
	}
	if err := json.Unmarshal(r.RequestedSchema, &form); err != nil {
		return form, err
	}
	if form.Type != "object" || form.Properties == nil {
		return form, fmt.Errorf("invalid elicitation form")
	}
	for _, key := range form.Required {
		if _, ok := form.Properties[key]; !ok {
			return form, fmt.Errorf("unknown required field: %s", key)
		}
	}
	for key, field := range form.Properties {
		switch field.Type {
		case "string", "number", "integer", "boolean":
		case "array":
			if field.Items == nil || len(field.Options()) == 0 {
				return form, fmt.Errorf("unsupported array: %s", key)
			}
		default:
			return form, fmt.Errorf("unsupported field: %s", key)
		}
	}
	return form, nil
}

func (r *Request) ElicitationURL() bool {
	u, err := url.Parse(r.URL)
	return r.Mode == "url" && err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http")
}

func (f ElicitationForm) Content(values map[string]string) (map[string]any, error) {
	content := map[string]any{}
	for _, key := range f.Keys() {
		field := f.Properties[key]
		value, exists := values[key]
		if !exists {
			if slices.Contains(f.Required, key) {
				return nil, fmt.Errorf("%s: required", key)
			}
			continue
		}
		var parsed any = value
		valid := true
		switch field.Type {
		case "boolean":
			parsed, _ = strconv.ParseBool(value)
			valid = value == "true" || value == "false"
		case "number", "integer":
			number, err := strconv.ParseFloat(value, 64)
			valid = err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
			valid = valid && (field.Type != "integer" || math.Trunc(number) == number)
			valid = valid && (field.Minimum == nil || number >= *field.Minimum) && (field.Maximum == nil || number <= *field.Maximum)
			parsed = number
		case "string":
			length := utf8.RuneCountInString(value)
			valid = (field.MinLength == nil || length >= *field.MinLength) && (field.MaxLength == nil || length <= *field.MaxLength)
			switch field.Format {
			case "email":
				address, err := mail.ParseAddress(value)
				valid = valid && err == nil && address.Address == value
			case "uri":
				u, err := url.Parse(value)
				valid = valid && err == nil && u.IsAbs()
			case "date":
				_, err := time.Parse("2006-01-02", value)
				valid = valid && err == nil
			case "date-time":
				_, err := time.Parse(time.RFC3339, value)
				valid = valid && err == nil
			}
			if field.Enum != nil || field.OneOf != nil {
				valid = valid && slices.ContainsFunc(field.Options(), func(o ElicitationOption) bool { return o.Value == value })
			}
		case "array":
			var selected []string
			err := json.Unmarshal([]byte(value), &selected)
			valid = err == nil && selected != nil
			valid = valid && (field.MinItems == nil || len(selected) >= *field.MinItems) && (field.MaxItems == nil || len(selected) <= *field.MaxItems)
			seen := map[string]bool{}
			for _, item := range selected {
				valid = valid && !seen[item] && slices.ContainsFunc(field.Options(), func(o ElicitationOption) bool { return o.Value == item })
				seen[item] = true
			}
			parsed = selected
		}
		if !valid {
			return nil, fmt.Errorf("%s: invalid value", key)
		}
		content[key] = parsed
	}
	return content, nil
}

type ElicitationResponse struct {
	Action  string          `json:"action"`
	Content any             `json:"content"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}
