package approval

import (
	"fmt"

	"github.com/juege/osh-prod-release/internal/models"
)

// Engine implements R3 (dual review + demo gate) and R6 (normal vs urgent boss approval).
type Engine struct {
	BossName string
}

func New(bossName string) *Engine {
	return &Engine{BossName: bossName}
}

// ItemReviewOK checks one change item has valid dual reviews.
func (e *Engine) ItemReviewOK(item models.ChangeItem) (bool, string) {
	if item.Reviewer1 == "" || item.Reviewer2 == "" {
		return false, "缺少两位评审人"
	}
	if item.Reviewer1 == item.Reviewer2 {
		return false, "两位评审人不能相同"
	}

	allowed := map[string]bool{item.Reviewer1: true, item.Reviewer2: true}
	approves := map[string]models.Review{}
	for _, rv := range item.Reviews {
		if rv.Result != models.ReviewApprove {
			continue
		}
		if !allowed[rv.Reviewer] {
			return false, fmt.Sprintf("评审人 %s 不在指定名单", rv.Reviewer)
		}
		if !rv.Tested {
			return false, fmt.Sprintf("评审人 %s 未确认已实测", rv.Reviewer)
		}
		if item.DemoRequired && item.Developer == rv.Reviewer {
			return false, "开发者不能自评通过，需另一位评审人确认"
		}
		if item.DemoRequired && rv.Reviewer != item.Developer {
			if !rv.DemoSeen {
				return false, fmt.Sprintf("评审人 %s 需确认已观看开发者演示", rv.Reviewer)
			}
		}
		approves[rv.Reviewer] = rv
	}

	if len(approves) < 2 {
		return false, "需要两位评审人都通过且已实测"
	}
	return true, ""
}

// AllItemsReviewOK checks every item in release.
func (e *Engine) AllItemsReviewOK(items []models.ChangeItem) (bool, string) {
	for _, it := range items {
		ok, msg := e.ItemReviewOK(it)
		if !ok {
			return false, fmt.Sprintf("[%s] %s", it.Title, msg)
		}
	}
	if len(items) == 0 {
		return false, "发布单至少包含一个上线项"
	}
	return true, ""
}

// CanStartDeploy checks reviews + boss approval; admins may bypass.
func (e *Engine) CanStartDeploy(rel models.Release, adminBypass bool) (bool, string) {
	if adminBypass {
		return true, ""
	}
	ok, msg := e.AllItemsReviewOK(rel.Items)
	if !ok {
		return false, msg
	}
	if !rel.BossApproved {
		return false, fmt.Sprintf("需要 %s 终审通过", e.BossName)
	}
	return true, ""
}

// NeedsPerItemBossApproval returns true for urgent releases.
func (e *Engine) NeedsPerItemBossApproval(level models.ReleaseLevel) bool {
	return level == models.LevelUrgent
}
