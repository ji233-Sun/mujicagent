package codex

import (
	"context"
	"encoding/json"
	"slices"
)

type PermissionProfile struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Allowed     bool   `json:"allowed"`
}

// Codex's named auto-review choice shares the workspace sandbox.
func (p PermissionProfile) WireID() string {
	if p.ID == ":approve-for-me" {
		return ":workspace"
	}
	return p.ID
}

func (p PermissionProfile) Reviewer() string {
	if p.ID == ":approve-for-me" {
		return "auto_review"
	}
	return "user"
}

func (p PermissionProfile) ApprovalPolicy() string {
	if p.ID == ":danger-full-access" {
		return "never"
	}
	return "on-request"
}

// Profiles are resolved against this project's config and managed requirements.
func (c *Client) PermissionProfiles(ctx context.Context, cwd string) ([]PermissionProfile, error) {
	var profiles []PermissionProfile
	var cursor *string
	for {
		var page struct {
			Data       []PermissionProfile `json:"data"`
			NextCursor *string             `json:"nextCursor"`
		}
		if err := c.Call(ctx, "permissionProfile/list", map[string]any{"cwd": cwd, "cursor": cursor}, &page); err != nil {
			return nil, err
		}
		profiles = append(profiles, page.Data...)
		cursor = page.NextCursor
		if cursor == nil || *cursor == "" {
			break
		}
	}
	var requirements struct {
		Requirements *struct {
			AllowedApprovalPolicies   []json.RawMessage `json:"allowedApprovalPolicies"`
			AllowedApprovalsReviewers []string          `json:"allowedApprovalsReviewers"`
		} `json:"requirements"`
	}
	if err := c.Call(ctx, "configRequirements/read", struct{}{}, &requirements); err != nil {
		return nil, err
	}
	autoReview, err := c.autoReviewEnabled(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if autoReview {
		for i, profile := range profiles {
			if profile.ID == ":workspace" {
				auto := PermissionProfile{ID: ":approve-for-me", Allowed: profile.Allowed}
				profiles = slices.Insert(profiles, i+1, auto)
				break
			}
		}
	}
	if r := requirements.Requirements; r != nil {
		for i := range profiles {
			allowed := r.AllowedApprovalPolicies == nil || slices.ContainsFunc(r.AllowedApprovalPolicies, func(raw json.RawMessage) bool {
				var policy string
				return json.Unmarshal(raw, &policy) == nil && policy == profiles[i].ApprovalPolicy()
			})
			reviewerAllowed := r.AllowedApprovalsReviewers == nil || slices.Contains(r.AllowedApprovalsReviewers, profiles[i].Reviewer())
			profiles[i].Allowed = profiles[i].Allowed && allowed && reviewerAllowed
		}
	}
	return profiles, nil
}

func (c *Client) autoReviewEnabled(ctx context.Context, cwd string) (bool, error) {
	enabled, available := false, false
	var cursor *string
	for {
		var page struct {
			Data []struct {
				Name, Stage string
				Enabled     bool
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "experimentalFeature/list", map[string]any{"cursor": cursor}, &page); err != nil {
			return false, err
		}
		for _, feature := range page.Data {
			if feature.Name == "guardian_approval" && feature.Stage != "removed" {
				available, enabled = true, feature.Enabled
			}
		}
		cursor = page.NextCursor
		if cursor == nil || *cursor == "" {
			break
		}
	}
	if !available {
		return false, nil
	}
	var result struct {
		Config struct {
			Features map[string]bool `json:"features"`
		} `json:"config"`
	}
	if err := c.Call(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &result); err != nil {
		return false, err
	}
	if override, ok := result.Config.Features["guardian_approval"]; ok {
		enabled = override
	}
	return enabled, nil
}

type NetworkPolicyAmendment struct {
	Action string `json:"action"`
	Host   string `json:"host"`
}

type ApprovalOption struct {
	Kind     string
	Prefix   []string
	Host     string
	Decision json.RawMessage
}

// Keep the server's decision payload intact, including its rule scope.
func approvalOption(raw json.RawMessage) (ApprovalOption, bool) {
	o := ApprovalOption{Decision: raw}
	if json.Unmarshal(raw, &o.Kind) == nil {
		return o, slices.Contains([]string{"accept", "acceptForSession", "decline", "cancel"}, o.Kind)
	}
	var value struct {
		Exec *struct {
			Prefix []string `json:"execpolicy_amendment"`
		} `json:"acceptWithExecpolicyAmendment"`
		Network *struct {
			Amendment NetworkPolicyAmendment `json:"network_policy_amendment"`
		} `json:"applyNetworkPolicyAmendment"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return o, false
	}
	if value.Exec != nil && len(value.Exec.Prefix) > 0 && value.Network == nil {
		o.Kind, o.Prefix = "execRule", value.Exec.Prefix
		return o, true
	}
	if value.Network != nil && value.Exec == nil {
		a := value.Network.Amendment
		if a.Host != "" && (a.Action == "allow" || a.Action == "deny") {
			o.Kind, o.Host = "network-"+a.Action, a.Host
			return o, true
		}
	}
	return o, false
}

func (r *Request) ApprovalOptions() []ApprovalOption {
	if r.Method != "item/commandExecution/requestApproval" && r.Method != "item/fileChange/requestApproval" {
		return nil
	}
	raw := r.AvailableDecisions
	if raw == nil {
		add := func(value any) { b, _ := json.Marshal(value); raw = append(raw, b) }
		add("accept")
		switch {
		case r.Method == "item/fileChange/requestApproval":
			add("acceptForSession")
			add("decline")
		case r.Kind == "humanCommand":
			add("decline")
		case r.NetworkApprovalContext != nil:
			add("acceptForSession")
			for _, a := range r.ProposedNetworkPolicyAmendments {
				if a.Action == "allow" {
					add(map[string]any{"applyNetworkPolicyAmendment": map[string]any{"network_policy_amendment": a}})
				}
			}
		case len(r.AdditionalPermissions) > 0 && string(r.AdditionalPermissions) != "null":
		case len(r.ProposedExecpolicyAmendment) > 0:
			add(map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": r.ProposedExecpolicyAmendment}})
		}
		add("cancel")
	}
	var options []ApprovalOption
	for _, decision := range raw {
		o, ok := approvalOption(decision)
		if !ok {
			continue
		}
		if r.Kind == "humanCommand" && o.Kind != "accept" && o.Kind != "decline" && o.Kind != "cancel" {
			continue
		}
		options = append(options, o)
	}
	return options
}
