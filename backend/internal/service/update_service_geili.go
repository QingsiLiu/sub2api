package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Geili 二开约定：本 fork 的镜像不允许在容器内“在线更新/回滚”二进制。
//
// 原因：官方更新器固定从 Wei-Shaw/sub2api 的 GitHub Release 下载官方二进制，
// 会把我们编进镜像里的自有前端整个冲掉；新版本一律通过 geili/main 合并上游 tag、
// 打 v<上游版本>-geili.<n> 标签、由 release.yml 产出 GHCR 镜像后经部署脚本上线。
//
// "检查更新"（CheckUpdate）保持不动：后台仍然会提示官方有新版本，
// 这就是“收到上游更新提示”的来源；只是“执行更新”被拒绝。
//
// 默认策略为 disabled。需要恢复官方行为时可在构建期覆盖：
//
//	go build -ldflags "-X github.com/Wei-Shaw/sub2api/internal/service.selfUpdatePolicy=enabled"
var selfUpdatePolicy = "disabled"

// ErrSelfUpdateDisabled 在线更新/回滚被本构建禁用时返回。
var ErrSelfUpdateDisabled = infraerrors.Forbidden(
	"SELF_UPDATE_DISABLED",
	"in-place self-update is disabled in this build; new versions are delivered as container images through the Geili release pipeline (see GEILI.md)",
)

// selfUpdateDisabled 报告当前构建是否禁止修改运行中的二进制。
func (s *UpdateService) selfUpdateDisabled() bool {
	return selfUpdatePolicy != "enabled"
}
