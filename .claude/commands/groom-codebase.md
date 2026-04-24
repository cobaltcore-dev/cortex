---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch
description: Groom the codebase by finding enhancements, bugs or other issues and fix them.
---

Groom the code base for potential enhancements, bugs, or other issues. Focus on making small, incremental improvements that can be easily reviewed and merged. This could include refactoring code for better readability, improving error handling, fixing minor bugs, optimizing performance in specific areas, or updating documentation to be more clear and comprehensive.

Perform a comprehensive review using subagents for key areas:

- bug-hunter

Note: Other agents are coming soon.

Use the collected information to identify specific, actionable improvements. For each improvement, create a new branch, implement the change, and open a pull request targeting main. Ensure that each PR is focused on a single logical change to facilitate review. Use clear, concise commit messages that describe the change without markdown or line breaks. If you find more than 3 issues, prioritize the most impactful ones and address those first. If no issues are found, simply conclude the grooming process without opening any PRs or issues.

---
