package gateway

import (
	"context"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/skill"
	"github.com/Forest-Isle/daimon/internal/tool"
	"log/slog"
)

type SkillSubsystem struct {
	Manager *skill.Manager
}

func (ss *SkillSubsystem) Name() string                  { return "skill" }
func (ss *SkillSubsystem) Start(_ context.Context) error { return nil }
func (ss *SkillSubsystem) Stop(_ context.Context) error  { return nil }

func InitSkills(features *FeatureSubsystem, cfg *config.Config, toolsReg *tool.Registry, builder *agent.DepsBuilder) *SkillSubsystem {
	ss := &SkillSubsystem{}
	if !features.IsEnabled("skills") {
		return ss
	}
	ss.Manager = skill.New()
	_ = ss.Manager.LoadBuiltin()
	_ = ss.Manager.LoadDir(defaultSkillsDir())
	stagingDir := defaultDistillStagingDir()
	for _, dir := range cfg.Skills.ExtraDirs {
		// The distill staging dir holds un-promoted SKILL.md drafts that must stay
		// inert (never auto-loaded/executed). Refuse to load it as an active skills
		// dir even if it is listed here, so a misconfig cannot defeat that isolation.
		if sameResolvedDir(dir, stagingDir) {
			slog.Warn("skill: refusing to load distill staging dir as active skills (drafts must stay inert)", "dir", dir)
			continue
		}
		_ = ss.Manager.LoadDir(dir)
	}
	builder.MultiAgent.SkillMgr = ss.Manager
	toolsReg.Register(tool.NewSkillTool(ss.Manager))
	slog.Info("skill manager initialized", "skills", len(ss.Manager.All()))
	return ss
}
