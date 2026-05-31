<?php

declare(strict_types=1);

namespace AgentHarness;

class SkillPromptMiddleware extends BaseMiddleware
{
    /** @var array<int, Skill> */
    private array $skills;

    /** @var array<int, Skill> */
    private array $pending;

    /**
     * @param array<int, Skill> $skills loaded skills (eager + activated) — full instructions.
     * @param array<int, Skill> $pending pending progressive skills — name + description + load hint.
     */
    public function __construct(array $skills, array $pending = [])
    {
        $this->skills = $skills;
        $this->pending = $pending;
    }

    public function pre(array $messages, mixed $context): array
    {
        $sections = [];
        foreach ($this->skills as $skill) {
            if ($skill->instructions !== '') {
                $sections[] = "## {$skill->name}\n{$skill->instructions}";
            }
        }
        foreach ($this->pending as $skill) {
            $sections[] = "## {$skill->name}\n{$skill->description}\n\n"
                . "_Not loaded — call `load_skill('{$skill->name}')` to activate._";
        }

        if (count($sections) === 0) {
            return $messages;
        }

        $block = "\n\n---\n**Available Skills:**";
        foreach ($sections as $section) {
            $block .= "\n\n{$section}";
        }

        foreach ($messages as &$message) {
            if (isset($message['role']) && $message['role'] === 'system') {
                $message['content'] = ($message['content'] ?? '') . $block;
                unset($message);
                return $messages;
            }
        }
        unset($message);

        array_unshift($messages, ['role' => 'system', 'content' => ltrim($block, "\n")]);
        return $messages;
    }
}
