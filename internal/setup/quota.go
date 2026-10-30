package setup

import (
	"context"
	"errors"

	"github.com/niparis/agent-net/internal/quotaaxi"
)

// These are variables so tests can run setup without npm or the network.
var (
	ensureQuotaAXI      = quotaaxi.Ensure
	ensureQuotaAXIAsync = quotaaxi.EnsureAsync
	removeQuotaAXI      = quotaaxi.Remove
)

const quotaStepName = "quota-axi"

// quotaAXI installs the private copy when the owner allowed quotas, or removes
// it when they turned the item off. Missing Node is reported in one line and
// never fails setup.
func (r *runner) quotaAXI(ctx context.Context) {
	if r.want == nil || !r.want.QuotaAXI {
		removed, err := removeQuotaAXI()
		switch {
		case err != nil:
			r.step(quotaStepName, "", err)
		case removed:
			r.step(quotaStepName, "off (removed the private copy)", nil)
		default:
			r.step(quotaStepName, "off", nil)
		}
		return
	}
	if _, ok := quotaaxi.UserPath(""); ok {
		r.step(quotaStepName, "ok (using the installed quota-axi)", nil)
		return
	}
	if _, ok := quotaaxi.Path(""); ok {
		r.step(quotaStepName, "ok (already installed)", nil)
		return
	}
	if err := ensureQuotaAXI(ctx, nil); err != nil {
		if errors.Is(err, quotaaxi.ErrNeedNode) {
			r.step(quotaStepName, quotaaxi.NeedNodeMessage, nil)
			return
		}
		r.step(quotaStepName, "", err)
		return
	}
	if _, ok := quotaaxi.Path(""); !ok {
		r.step(quotaStepName, "skipped (install failed earlier today, trying again tomorrow)", nil)
		return
	}
	r.step(quotaStepName, "ok (installed "+quotaaxi.Version+")", nil)
}
