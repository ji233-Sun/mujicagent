package controller

import "github.com/ji233-Sun/mujicagent/internal/codex"

func (c *Controller) RespondElicitation(r *codex.Request, action string, values map[string]string) {
	if r.Method != "mcpServer/elicitation/request" {
		return
	}
	response := codex.ElicitationResponse{Action: action}
	switch action {
	case "accept":
		if r.Mode == "url" {
			if !r.ElicitationURL() {
				return
			}
		} else {
			form, err := r.ElicitationForm()
			if err != nil {
				return
			}
			response.Content, err = form.Content(values)
			if err != nil {
				return
			}
		}
	case "decline", "cancel":
	default:
		return
	}
	// Request metadata belongs to the server; it is not client response metadata.
	c.replyRequest(r, response)
}
