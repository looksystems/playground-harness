<?php

declare(strict_types=1);

namespace AgentHarness;

class SkillPromptMiddleware extends BaseMiddleware
{
    /** @var array<int, Skill> */
    private array $skills;

    /**
     * @param array<int, Skill> $skills
     */
    public function __construct(array $skills)
    {
        $this->skills = $skills;
    }

    public function pre(array $messages, mixed $context): array
    {
        $skillsWithInstructions = array_filter(
            $this->skills,
            fn(Skill $s) => $s->instructions !== '',
        );

        if (count($skillsWithInstructions) === 0) {
            return $messages;
        }

        $block = "\n\n---\n**Available Skills:**";
        foreach ($skillsWithInstructions as $skill) {
            $block .= "\n\n## {$skill->name}\n{$skill->instructions}";
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
