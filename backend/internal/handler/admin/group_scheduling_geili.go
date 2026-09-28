package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
	"strings"
)

func (h *GroupHandler) groupSchedulingAdminGeili(c *gin.Context) service.GroupSchedulingAdminGeili {
	admin, ok := h.adminService.(service.GroupSchedulingAdminGeili)
	if !ok {
		response.Error(c, 503, "Group scheduling API unavailable")
		return nil
	}
	return admin
}

func (h *GroupHandler) GroupSchedulingSnapshotGeili(c *gin.Context) {
	admin := h.groupSchedulingAdminGeili(c)
	if admin == nil {
		return
	}
	raw := strings.Split(c.Query("group_ids"), ",")
	ids := make([]int64, 0, len(raw))
	if len(raw) > 64 {
		response.BadRequest(c, "最多查询64个分组")
		return
	}
	seen := map[int64]bool{}
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "group_ids 必须是正整数列表")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	result, err := admin.GroupSchedulingSnapshotGeili(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GroupHandler) UpdateGroupSchedulingGeili(c *gin.Context) {
	admin := h.groupSchedulingAdminGeili(c)
	if admin == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效分组 ID")
		return
	}
	var input service.GroupSchedulingUpdateGeili
	if err = c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "无效调度设置")
		return
	}
	result, err := admin.SetGroupSchedulingGeili(c.Request.Context(), id, input, adminActorScope(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GroupHandler) PreviewGroupSchedulingGeili(c *gin.Context) {
	admin := h.groupSchedulingAdminGeili(c)
	if admin == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效分组 ID")
		return
	}
	var input service.GroupSchedulingPreviewGeili
	if err = c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "无效预览参数")
		return
	}
	result, err := admin.PreviewGroupSchedulingGeili(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"preview_only": true, "note": "无会话/计费上下文，不占并发、不建立粘性、不调用上游；不保证下一请求选中该顺位", "snapshot": result})
}
