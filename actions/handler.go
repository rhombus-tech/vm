// actions/handler.go (or where you process actions)
import (
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/avalanchego/ids"
    "github.com/rhombus-tech/hypersdk/x/contracts/runtime/time"
)

type ActionHandler struct {
    // Add time manager
    timeManager *time.Manager
    // Your existing fields
}

func (h *ActionHandler) ExecuteAction(ctx context.Context, action Action) error {
    // Get verified time
    verifiedTime, err := h.timeManager.GetVerifiedTime()
    if err != nil {
        return fmt.Errorf("time verification failed: %w", err)
    }

    // Create verified entry
    entry := &time.VerifiedEntry{
        ActionID:     action.ID(),
        VerifiedTime: verifiedTime,
        BlockHeight:  h.blockHeight, // or however you track height
    }

    // Store in sequence
    h.timeManager.sequence.AddEntry(entry)

    // Continue with normal action execution
    return h.executeActionWithTime(ctx, action, verifiedTime)
}
