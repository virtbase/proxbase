# Changelog

## [0.3.0](https://github.com/virtbase/proxbase/compare/v0.2.2...v0.3.0) (2026-10-22)


### Features

* cluster owner and lifetime, proxbase gc ([#30](https://github.com/virtbase/proxbase/issues/30)) ([7e20e91](https://github.com/virtbase/proxbase/commit/7e20e916959d66f4f03a090128708207acf9271b))
* cluster_plan, capacity warnings and automatic port ranges ([#29](https://github.com/virtbase/proxbase/issues/29)) ([9125158](https://github.com/virtbase/proxbase/commit/91251584b1439558acf194db5ae3f3ba31e019f3))
* **mcp:** cluster_diagnose collects what explains a problem ([#23](https://github.com/virtbase/proxbase/issues/23)) ([91b02fc](https://github.com/virtbase/proxbase/commit/91b02fc6b2887b5889b2e986c81d6482ef35c369))
* **mcp:** cluster_reset and a baseline snapshot after create ([#24](https://github.com/virtbase/proxbase/issues/24)) ([a628f39](https://github.com/virtbase/proxbase/commit/a628f3978bea2843aa5740fd0df1c82b72173006))
* **mcp:** node, image, host and file tools; proxbase cp ([#27](https://github.com/virtbase/proxbase/issues/27)) ([7317f01](https://github.com/virtbase/proxbase/commit/7317f018e8c210017851f167f52b39ddacc57b30))
* **mcp:** prompts, resource templates, completions and elicitation ([#34](https://github.com/virtbase/proxbase/issues/34)) ([2722cae](https://github.com/virtbase/proxbase/commit/2722cae5d4230ca6a55693b4d1e951d9008b1b8e))
* **mcp:** pve_get and pve_call tools for the Proxmox VE API ([#22](https://github.com/virtbase/proxbase/issues/22)) ([17ed12b](https://github.com/virtbase/proxbase/commit/17ed12b8f48f65ee0252aecb7ae52485140c6315))
* **mcp:** read-only mode, toolsets and an audit log ([#32](https://github.com/virtbase/proxbase/issues/32)) ([0a34022](https://github.com/virtbase/proxbase/commit/0a34022b039eb89137218e0da5aaba3ce1df952f))
* **mcp:** Streamable HTTP transport for remote agents ([#35](https://github.com/virtbase/proxbase/issues/35)) ([3db1348](https://github.com/virtbase/proxbase/commit/3db1348438c43ca61aaa3e3e523481c3278bf957))
* **mcp:** task_list, task_log and task_wait ([#26](https://github.com/virtbase/proxbase/issues/26)) ([faf084f](https://github.com/virtbase/proxbase/commit/faf084fdf362c7138108f18431826c57be1133b7))
* proxbase agent skill and Claude Code plugin ([#25](https://github.com/virtbase/proxbase/issues/25)) ([ea78238](https://github.com/virtbase/proxbase/commit/ea782385eb5923bc55f3585962041300f07c0821))
* proxbase upgrade updates the CLI to the latest release ([#28](https://github.com/virtbase/proxbase/issues/28)) ([a089fbb](https://github.com/virtbase/proxbase/commit/a089fbb36207d6e616bbf5b456dea3a5a019b615))
* scoped API tokens for least-privilege tests ([#31](https://github.com/virtbase/proxbase/issues/31)) ([173abd5](https://github.com/virtbase/proxbase/commit/173abd5720a40f8a91c2bddb440505bf20ccdc40))
* VM and container templates as fixtures ([#33](https://github.com/virtbase/proxbase/issues/33)) ([e18fbf9](https://github.com/virtbase/proxbase/commit/e18fbf9430f70ec0b20b824a5b7376dee45f3a81))


### Bug Fixes

* **job:** recognize a job right after it started ([#37](https://github.com/virtbase/proxbase/issues/37)) ([2856f05](https://github.com/virtbase/proxbase/commit/2856f0580054f7c4b9ee2fe2a8bbcfb42116bb9d))
* **mcp:** return tool calls before the client gives up ([#21](https://github.com/virtbase/proxbase/issues/21)) ([62ed430](https://github.com/virtbase/proxbase/commit/62ed4301e4745d0b2bbe882e50487d791b570eb2))


### Documentation

* AGENTS.md, llms.txt and Copilot coding agent setup ([#36](https://github.com/virtbase/proxbase/issues/36)) ([86e1081](https://github.com/virtbase/proxbase/commit/86e108182fde5fa0e0b4838b8e286842942e292a))
* fix create sequence diagram rendering ([#18](https://github.com/virtbase/proxbase/issues/18)) ([a2a9dd4](https://github.com/virtbase/proxbase/commit/a2a9dd4946b1ae3b336b90afc2a30b134a3ae575))
* research on agent workflows, product variations and upgrades ([#20](https://github.com/virtbase/proxbase/issues/20)) ([5c4a961](https://github.com/virtbase/proxbase/commit/5c4a96150b1901ada271acd119844c275fcfd14b))

## [0.2.2](https://github.com/virtbase/proxbase/compare/v0.2.1...v0.2.2) (2026-10-22)


### Bug Fixes

* **action:** wait for udev before checking /dev/kvm ([#14](https://github.com/virtbase/proxbase/issues/14)) ([b899826](https://github.com/virtbase/proxbase/commit/b8998264cddbdf00df1acbc0c6d2cac938225781))


### Dependencies

* **deps:** update container images ([#16](https://github.com/virtbase/proxbase/issues/16)) ([737f410](https://github.com/virtbase/proxbase/commit/737f41043ee5b8d75c4606af4e18e8306340b551))
* update go modules ([#12](https://github.com/virtbase/proxbase/issues/12)) ([f659167](https://github.com/virtbase/proxbase/commit/f659167236a3e644e68c4cdb3c62d31337143a4c))

## [0.2.1](https://github.com/virtbase/proxbase/compare/v0.2.0...v0.2.1) (2026-10-11)


### Performance

* faster provisioning and lower resource use ([#8](https://github.com/virtbase/proxbase/issues/8)) ([076717d](https://github.com/virtbase/proxbase/commit/076717d7bc7fa812145c87f9e09950c7f4343f52))

## [0.2.0](https://github.com/virtbase/proxbase/compare/v0.1.0...v0.2.0) (2026-10-09)


### Features

* MCP server, progress json and GitHub Action ([a269351](https://github.com/virtbase/proxbase/commit/a2693513cde439b3c73c14db9c28d27e987c5df5))
* MCP server, progress json and GitHub Action ([39741b3](https://github.com/virtbase/proxbase/commit/39741b38f299b69c0cd353a6ffe7818d06c1d678))


### Bug Fixes

* tagging ([92eb927](https://github.com/virtbase/proxbase/commit/92eb92759af6468cf23aea99d5f3a789aa5b47be))

## 0.1.0 (2026-10-09)


### Features

* add docker mode ([b298b83](https://github.com/virtbase/proxbase/commit/b298b83ffd6e74d71585024ebd61471c364b37c5))
* commit current state ([38e3806](https://github.com/virtbase/proxbase/commit/38e380601c8f68ccd61bba368dadc9ae5374f2bb))
* install relevant golang skills ([cc5f25b](https://github.com/virtbase/proxbase/commit/cc5f25be9fffc4b6a9a831eca6cd9a085eed0e04))
* setup repo tooling and refactor ([55df8c0](https://github.com/virtbase/proxbase/commit/55df8c0bd5697e0d0b3c5af82b1e5872258bcb32))
* snapshots and lifecycle options ([782fc4a](https://github.com/virtbase/proxbase/commit/782fc4aae1b235b231cb7ba746d655289c3be22a))
