package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initSkillManager() error {
	if !gw.featureEnabled("skills") {
		return nil
	}

	gw.skillMgr = skill.New()
	if err := gw.skillMgr.LoadBuiltin(); err != nil {
		slog.Warn("gateway: failed to load builtin skills", "err", err)
	}
	userSkillsDir := defaultSkillsDir()
	if err := gw.skillMgr.LoadDir(userSkillsDir); err != nil {
		slog.Warn("gateway: failed to load user skills", "dir", userSkillsDir, "err", err)
	}
	for _, dir := range gw.cfg.Skills.ExtraDirs {
		if err := gw.skillMgr.LoadDir(dir); err != nil {
			slog.Warn("gateway: failed to load extra skills dir", "dir", dir, "err", err)
		}
	}
	gw.runtime.SetSkillManager(gw.skillMgr)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetSkillManager(gw.skillMgr)
	}
	// Register the read_skill tool for progressive disclosure —
	// agent sees metadata in prompt, loads full content via this tool.
	gw.tools.Register(tool.NewSkillTool(gw.skillMgr))
	slog.Info("skill manager initialized", "skills", len(gw.skillMgr.All()))

	return nil
}
