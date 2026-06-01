
## Rules

* **Question Tool**: Refers to the built-in tools within various CLIs, such as `AskUserQuestion`, `question`, or `ask_user`.
 
* **Show Must Go On**: Whenever you are about to conclude the current turn (equivalent to the timing of the Claude Code `Stop` hook), you must use the **Question Tool** to propose 2–4 specific next-step options. Ending a turn with only "Completed" or an open-ended "Should we continue?" is prohibited. This rule is only waived if the user explicitly says "End" or "That's it."

once complete, use question tool to ask for my review or next instruction