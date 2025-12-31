# AI Development Notes

Notes and observations about working with AI assistants on this codebase.

## Best Practices

* **Closed loop testing** - Be sure to get the AI to make changes to the source and then test its own changes. This catches errors immediately rather than letting them accumulate.

* **AI strengths for tedious work** - Some things are easy for the AI, like picking through large output of breadcrumbs or running tests carefully after each change and never assuming something will work. Example: "Here are the breadcrumbs from several hundred system calls, find the one that has mismatched entry and exit behavior."

* **Using plans is critical for money saving** - Planning before implementation reduces wasted tokens on wrong approaches.

* **Using todos is great for being specific about which things in what order** - Keeps both the AI and human aligned on progress and next steps.

* **Git worktrees tradeoffs** - Git worktrees are probably not as valuable if you are building things yourself because they are equivalent to commits on the "main" branch if you are the only person using the worktrees. However, the AI is good at untangling worktree blunders like having 5 worktrees with uncommitted changes from the same source commit.

* **Incremental changes with working code** - Incrementally changing something while keeping the code working is easier because the cost of "dead work" such as building transpilers and shims is close to zero. Example: the transition of the assembly code from GCC assembly to Plan9/Go assembly.
