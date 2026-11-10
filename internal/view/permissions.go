package view

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (v *View) permissionName(id string) string {
	tx := v.texts()
	switch id {
	case ":read-only":
		return tx.ReadOnly()
	case ":workspace":
		return tx.DefaultPermissions()
	case ":approve-for-me":
		return tx.ApproveForMe()
	case ":danger-full-access":
		return tx.FullAccess()
	default:
		return id
	}
}

func permissionIcon(id string) *ui.SVG {
	switch id {
	case ":workspace":
		return shieldCheckIcon
	case ":approve-for-me":
		return thinkingIcon
	case ":danger-full-access":
		return ruleIcon
	default:
		return shieldIcon
	}
}

func (v *View) permissionSelector(c *ui.Context) {
	tx := v.texts()
	if v.permissionTarget != v.C.State.Selected {
		v.permissionPicker = false
	}
	label := v.permissionName(v.C.PermissionID())
	if v.C.NextPermissionID() != v.C.PermissionID() {
		label += " → " + v.permissionName(v.C.NextPermissionID())
	}
	button := pickerOption(c.Key("permission-selector"), tx.Permissions(), v.permissionPicker).
		Height(30).MaxWidth(170).Shrink(0).Tooltip(label).
		Disabled(v.C.Connection != "Connected" || v.C.PermissionUpdating())
	button.Children(func() {
		ui.Icon(c, permissionIcon(v.C.PermissionID())).Size(15, 15).Shrink(0)
		ui.Text(c, label).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
	})
	button.OnClick(func() {
		v.permissionPicker = !v.permissionPicker
		v.modelPicker = false
		v.permissionTarget = v.C.State.Selected
		if v.permissionPicker {
			v.C.LoadPermissions()
		}
	})
	pickerPopover(c.Key("permission-picker"), button, &v.permissionPicker, tx.PermissionPicker(), 290, func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, tx.Permissions()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8)
			if v.C.NextPermissionID() != v.C.PermissionID() {
				ui.Text(c, tx.PermissionTiming()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8)
				ui.Text(c, tx.PermissionNextTurn(v.permissionName(v.C.NextPermissionID()))).FontSize(12).Padding(6, 8)
			}
			if v.C.PermissionsLoading {
				ui.Spinner(c).Size(18, 18).Margin(12)
				return
			}
			if v.C.PermissionsError != "" {
				ui.Text(c, v.C.PermissionsError).FontSize(12).TextColor(c.Theme().Danger).Padding(8)
				ui.Button(c, tx.Retry()).OnClick(v.C.LoadPermissions)
				return
			}
			for _, profile := range v.C.Permissions {
				label := v.permissionName(profile.ID)
				detail := profile.Description
				if profile.ID == ":approve-for-me" {
					detail = tx.AutoReviewHint()
				}
				if !profile.Allowed {
					detail = tx.PermissionRestricted()
				}
				selected := profile.ID == v.C.NextPermissionID()
				b := pickerOption(c.Key(profile.ID), label, selected).Tooltip(detail).Disabled(!profile.Allowed)
				b.Children(func() {
					ui.Icon(c, permissionIcon(profile.ID)).Size(17, 17).Shrink(0)
					ui.Text(c, label).FontSize(13).Grow(1).SingleLine()
					if selected {
						ui.Icon(c, checkIcon).Size(14, 14)
					}
				})
				b.OnClick(func() {
					v.permissionPicker = false
					if profile.ID == ":danger-full-access" && !selected {
						v.confirmFullAccess = true
						return
					}
					v.C.SetPermission(profile.ID)
				})
			}
		})
	})
}

func (v *View) approvalOptions(c *ui.Context, r *codex.Request) {
	tx := v.texts()
	options := r.ApprovalOptions()
	if len(options) == 0 {
		ui.Text(c, tx.NoApprovalOptions()).TextColor(c.Theme().TextMuted)
		return
	}
	ui.Column(c).Gap(3).Children(func() {
		for i, option := range options {
			label, detail, icon := "", "", shieldIcon
			switch option.Kind {
			case "accept":
				label, icon = tx.AllowOnce(), checkIcon
			case "acceptForSession":
				label, icon = tx.AllowSession(), shieldCheckIcon
			case "decline":
				label, icon = tx.Deny(), closeIcon
			case "cancel":
				label, icon = tx.DenyAndStop(), failureIcon
			case "execRule":
				label, detail, icon = tx.AllowCommandRule(), strings.Join(option.Prefix, " "), ruleIcon
			case "network-allow":
				label, detail, icon = tx.AllowHost(), option.Host, shieldCheckIcon
			case "network-deny":
				label, detail, icon = tx.DenyHost(), option.Host, failureIcon
			}
			b := approvalRow(c.Key(i), label, detail, icon, option.Kind == "accept").Disabled(r.Responding)
			if option.Kind == "acceptForSession" {
				b.Tooltip(tx.SessionApprovalHint())
			}
			if detail != "" {
				b.Tooltip(detail)
			}
			b.OnClick(func() { v.C.Approve(r, option.Decision) })
		}
	})
}

// approvalRow keeps command, file and MCP choices visually consistent.
func approvalRow(c *ui.Context, label, detail string, icon *ui.SVG, selected bool) ui.Element {
	b := pickerOption(c, label, selected)
	if detail != "" {
		b.Height(52)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(16, 16).Shrink(0)
		ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, label).FontSize(12)
			if detail != "" {
				ui.Text(c, detail).Font("monospace").FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			}
		})
	})
	return b
}
