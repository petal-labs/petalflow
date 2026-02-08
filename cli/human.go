package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/petal-labs/petalflow/nodes"
)

// cliHumanHandler implements nodes.HumanHandler for non-interactive CLI runs.
// It auto-approves "approval" requests (the typical agent workflow gate) and
// returns an error for interactive types that require real human input.
type cliHumanHandler struct {
	w io.Writer // stderr — warnings go here, not stdout
}

func (h *cliHumanHandler) Request(_ context.Context, req *nodes.HumanRequest) (*nodes.HumanResponse, error) {
	if req.Type == nodes.HumanRequestApproval {
		fmt.Fprintf(h.w, "Auto-approving human approval request %q (non-interactive CLI mode)\n", req.ID)
		return &nodes.HumanResponse{
			RequestID:   req.ID,
			Choice:      "approve",
			Approved:    true,
			RespondedBy: "cli-auto",
			RespondedAt: time.Now(),
		}, nil
	}
	return nil, fmt.Errorf("human node %q requires interactive %s input, which is not supported in CLI mode", req.ID, req.Type)
}

var _ nodes.HumanHandler = (*cliHumanHandler)(nil)
