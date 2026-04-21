package skills

// Base provides default no-op implementations of the required Skill interface.
// Embed Base into your own skill struct and override the methods you need.
//
// Embedding Base means Name() returns "", so SkillManager will call AutoName
// to derive the skill's name from the concrete struct type. Override Name() to
// provide an explicit name instead.
//
// Example:
//
//	type WebBrowsingSkill struct {
//	    skills.Base
//	}
//	// Name is auto-derived: "web_browsing"
//
//	type MySkill struct {
//	    skills.Base
//	}
//	func (MySkill) Name() string { return "my_custom_name" }
type Base struct{}

// Name returns "" to signal that AutoName should derive the name from the
// concrete type. Override to provide an explicit name.
func (Base) Name() string { return "" }

// Description returns an empty string. Override to describe your skill.
func (Base) Description() string { return "" }

// Version returns "0.1.0", matching the Python Skill default. Override to
// declare the skill's version.
func (Base) Version() string { return "0.1.0" }

// Instructions returns an empty string. Override to provide system-prompt
// instructions that SkillPromptMiddleware will inject for your skill.
func (Base) Instructions() string { return "" }
