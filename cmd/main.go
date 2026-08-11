// EN: Command pigo is the CLI entry point for the pigo agent. It parses flags,
// 中文: 命令pigo 是pigo 代理的CLI 入口点。它解析标志，
// EN: overlays config.toml, and dispatches to one of the run modes — interactive
// 中文: 覆盖 config.toml，并分派到一种运行模式 — 交互
// EN: REPL, headless print, session listing, or the internal sub-agent RPC server:
// 中文: REPL、无头打印、会话列表或内部子代理 RPC 服务器：
//
// EN: pigo                                          # interactive REPL (on a TTY)
// 中文: Pigo # 交互式 REPL（在 TTY 上）
// EN: pigo -p "read README and summarize"           # print mode: final text
// 中文: Pigo -p“阅读自述文件并总结”#打印模式：最终文本
// EN: pigo -p "..." --output-format stream-json      # line-delimited JSON events
// 中文: Pigo -p "..." --output-format stream-json # 行分隔的 JSON 事件
// EN: pigo install <pkg> | list | uninstall | update # package management
// 中文: Pigo 安装 <pkg> |列表 |卸载 |更新#包管理
//
// EN: The provider is resolved from --model against the built-in OpenAI-compatible
// 中文: 提供程序是根据内置 OpenAI 兼容的 --model 解析的
// EN: gateways (OpenRouter by default, Ollama for local models), with the API key
// 中文: 网关（默认为 OpenRouter，本地模型为 Ollama），带有 API 密钥
// EN: taken from the environment. The process exit code reflects success (0) or
// 中文: 取自环境。进程退出代码反映成功（0）或
// EN: failure (1), so the command composes cleanly in pipelines. All run-assembly,
// 中文: 失败 (1)，因此该命令在管道中干净地组合。所有运行组装，
// EN: REPL, headless, and config logic lives under internal/cli/*; this file keeps
// 中文: REPL、无头和配置逻辑位于internal/cli/* 下；该文件保留
// EN: only flag parsing (cliOptions), config overlay (applyFileConfig), and the
// 中文: 仅标志解析 (cliOptions)、配置覆盖 (applyFileConfig) 和
// EN: dispatch seam that wires those subpackages together.
// 中文: 将这些子包连接在一起的调度接缝。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/cli/pkgcmd"
	"github.com/smallnest/pigo/internal/cli/repl"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/cli/tui"
	"github.com/smallnest/pigo/internal/cli/ui"
	"github.com/smallnest/pigo/internal/dream"
	"github.com/smallnest/pigo/internal/selfupdate"
)

// EN: Build metadata, injected at release time via -ldflags by goreleaser
// 中文: 构建元数据，由 goreleaser 在发布时通过 -ldflags 注入
// EN: (see .goreleaser.yaml). They keep their default values for `go build`/
// 中文: （参见 .goreleaser.yaml）。他们保留“go build”的默认值/
// EN: `go run` from source, so `pigo --version` still works without a release build.
// 中文: 从源代码“go run”，因此“pigo --version”在没有发布版本的情况下仍然可以工作。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// EN: cliOptions is the parsed command line, produced by main() and consumed by
// 中文: cliOptions 是解析后的命令行，由 main() 生成并由
// EN: dispatch. Separating parse from dispatch makes the dispatch logic testable
// 中文: 派遣。将解析与调度分开使得调度逻辑可测试
// EN: without touching the global flag set.
// 中文: 不触及全局标志集。
type cliOptions struct {
	prompt   string
	model    string
	baseURL  string
	apiKey   string
	protocol string
	// EN: provider, when non-empty, selects a built-in provider by name from the
	// 中文: 提供者，当非空时，从名称中选择一个内置提供者
	// EN: registry (mirrors pi's provider selection): provider.ResolveProvider then builds the
	// 中文: 注册表（镜像 pi 的提供程序选择）：provider.ResolveProvider 然后构建
	// EN: matching wire driver using the provider's default base URL, protocol, and
	// 中文: 使用提供商的默认基本 URL、协议和匹配的线路驱动程序
	// EN: API-key env var, ignoring the model-id heuristics.
	// 中文: API 密钥环境变量，忽略模型 ID 启发式。
	provider     string
	outputFmt    string
	noTools      bool
	listSessions bool
	resumeID     string
	continueLast bool
	// EN: approve grants the launch directory session-level trust up front (mirrors pi's
	// 中文: 批准预先授予启动目录会话级信任（镜像 pi 的
	// EN: --approve/-a): the first-launch trust prompt is skipped and side-effect
	// 中文: --approve/-a)：跳过首次启动信任提示并产生副作用
	// EN: tools (bash/write/edit) run without per-call confirmation for this run.
	// 中文: 工具（bash/写入/编辑）运行时无需每次调用​​确认。
	approve bool
	// EN: noSkills disables skill discovery (mirrors pi's --no-skills): skills under
	// 中文: noSkills 禁用技能发现（镜像 pi 的 --no-skills）：以下技能
	// EN: ~/.agents/skills are not loaded as /skill-name commands.
	// 中文: ~/.agents/skills 不会作为 /skill-name 命令加载。
	noSkills bool
	// EN: systemPrompt, when non-empty, replaces the default coding-assistant base
	// 中文: systemPrompt，当非空时，替换默认的编码辅助基础
	// EN: instruction (mirrors pi's --system-prompt). The environment block and
	// 中文: 指令（镜像 pi 的 --system-prompt）。环境块和
	// EN: AGENTS.md injection still apply on top of it.
	// 中文: AGENTS.md 注入仍然适用于它。
	systemPrompt string
	// EN: appendSystemPrompt holds --append-system-prompt values (mirrors pi, repeatable):
	// 中文: appendSystemPrompt 保存 --append-system-prompt 值（镜像 pi，可重复）：
	// EN: each is a path to a file whose contents are appended, or literal text when
	// 中文: every 是一个文件的路径，其内容被附加，或者文字文本
	// EN: it is not an existing file. Appended after the base prompt and AGENTS.md.
	// 中文: 它不是现有文件。附加在基本提示符和 AGENTS.md 之后。
	appendSystemPrompt []string
	// EN: configPrompts holds prompt-template paths from the config.toml `prompts`
	// 中文: configPrompts 保存来自 config.toml `prompts` 的提示模板路径
	// EN: array (settings tier); each is a file or directory loaded non-recursively.
	// 中文: 数组（设置层）；每个都是非递归加载的文件或目录。
	// EN: Populated by applyFileConfig; empty when the config omits `prompts`.
	// 中文: 由 applyFileConfig 填充；当配置省略“提示”时为空。
	configPrompts []string
	// EN: promptTemplates holds --prompt-template paths (CLI tier, repeatable); each
	// 中文: PromptTemplates 包含 --prompt-template 路径（CLI 层，可重复）；每个
	// EN: is a file or directory loaded non-recursively.
	// 中文: 是非递归加载的文件或目录。
	promptTemplates []string
	// EN: noPromptTemplates disables all prompt-template discovery (global, project,
	// 中文: noPromptTemplates 禁用所有提示模板发现（全局、项目、
	// EN: settings, CLI); built-in slash commands are unaffected. Independent of
	// 中文: 设置、CLI）；内置斜杠命令不受影响。独立于
	// EN: --no-skills.
	// 中文: ——无技能。
	noPromptTemplates bool
	// EN: subagentRPC selects the process-isolated sub-agent server mode (US-019,
	// 中文: subagentRPC选择进程隔离的子代理服务器模式（US-019，
	// EN: #135): pigo reads JSON-RPC sub-agent run requests from stdin and writes
	// 中文: #135):pigo 从 stdin 读取 JSON-RPC 子代理运行请求并写入
	// EN: results to stdout. Internal, used by SubAgentTool's process mode.
	// 中文: 结果到标准输出。内部，由 SubAgentTool 的进程模式使用。
	subagentRPC bool
	// EN: dream, when set, runs the process-isolated memory-consolidation pass and
	// 中文: dream 设置后，运行进程隔离的内存整合过程，
	// EN: exits: pigo enumerates + consolidates the global/project memory scope, emits
	// 中文: 退出：pigo 枚举+合并全局/项目内存范围，发出
	// EN: a single-line Report JSON on stdout, and exits 0/1. Internal, spawned by the
	// 中文: 标准输出上的单行报告 JSON，并退出 0/1。内部，产生于
	// EN: dream scheduler (and usable headlessly by scripts). See internal/dream and
	// 中文: 梦想调度程序（并且可以通过脚本无头使用）。参见内部/梦想和
	// EN: SPEC §4.1/§4.2.
	// 中文: 规范§4.1/§4.2。
	dream bool
	// EN: dreamDryRun pairs with --dream: analyze and report without writing files or
	// 中文: dreamDryRun 与 --dream 配对：分析和报告，无需写入文件或
	// EN: updating dream state (the lock is still taken). SPEC §5.5 dry-run row.
	// 中文: 更新梦想状态（锁仍然被占用）。 SPEC §5.5 试运行行。
	dreamDryRun bool
	// EN: thinkingLevel, when non-empty, is the --thinking-level flag: the reasoning
	// 中文: thinkingLevel，当非空时，是 --thinking-level 标志：推理
	// EN: effort for requests (off|minimal|low|medium|high|xhigh|max). It is the highest-
	// 中文: 请求的努力（off|minimal|low|medium|high|xhigh|max）。这是最高的——
	// EN: precedence layer in resolveThinkingLevel, overriding PIGO_THINKING_LEVEL, the
	// 中文: 优先层在resolveThinkingLevel中，覆盖PIGO_THINKING_LEVEL，
	// EN: config files, and the built-in default (medium).
	// 中文: 配置文件和内置默认值（中）。
	thinkingLevel string
	// EN: showVersion prints build metadata (version/commit/date, injected at release
	// 中文: showVersion 打印构建元数据（版本/提交/日期，在发布时注入
	// EN: time by goreleaser) and exits, without running the agent.
	// 中文: time by goreleaser）并退出，而不运行代理。
	showVersion bool
	// EN: noTUI forces the line-based REPL instead of the full-screen TUI (US-001).
	// 中文: noTUI 强制使用基于行的 REPL，而不是全屏 TUI (US-001)。
	// EN: When set — or when stdout is not a TTY — the no-prompt path falls back to
	// 中文: 当设置时——或者当 stdout 不是 TTY 时——无提示路径会回退到
	// EN: repl.Run rather than launching tui.Run.
	// 中文: repl.Run 而不是启动 tui.Run。
	noTUI bool
	// EN: cwd, when non-empty, is the working directory pigo switches to before doing
	// 中文: cwd，当非空时，是pigo在执行操作之前切换到的工作目录
	// EN: anything else (matches the Claude Agent SDK's cwd option / git -C). Every
	// 中文: 其他任何内容（与 Claude Agent SDK 的 cwd 选项 / git -C 匹配）。每一个
	// EN: cwd-derived resolution — built-in tool file roots, project trust, hooks
	// 中文: cwd 派生解析 — 内置工具文件根、项目信任、挂钩
	// EN: project dir, .pigo/ project config, git info, the status-bar path — reads
	// 中文: 项目目录、.pigo/ 项目配置、git 信息、状态栏路径 — 读取
	// EN: os.Getwd(), so a single os.Chdir here makes all of them operate in the
	// 中文: os.Getwd()，因此这里的单个 os.Chdir 使它们全部在
	// EN: given directory. This is what makes pigo usable as an SDK backend that can
	// 中文: 给定的目录。这就是 Pigo 可以用作 SDK 后端的原因
	// EN: be pointed at an arbitrary project root.
	// 中文: 指向任意项目根。
	cwd string
	// EN: memory holds the resolved [memory]/[checkpoint]/[compaction] config tables
	// 中文: 内存保存已解析的[内存]/[检查点]/[压缩]配置表
	// EN: (defaults applied, string forms parsed). These have no CLI flags — the
	// 中文: （应用默认值，解析字符串形式）。这些没有 CLI 标志 —
	// EN: config file is their only source — so applyFileConfig always populates this
	// 中文: 配置文件是它们唯一的来源 - 所以 applyFileConfig 总是填充这个
	// EN: (defaults when the tables are absent) for downstream memory/checkpoint/
	// 中文: （当表不存在时默认）下游内存/检查点/
	// EN: compaction wiring to consume. See config.MemorySettings.
	// 中文: 压紧接线消耗。请参阅 config.MemorySettings。
	memory config.MemorySettings
	// EN: dreamCfg is the resolved [dream] configuration (enabled / interval /
	// 中文: dreamCfg 是已解析的[dream]配置（启用/间隔/
	// EN: recent-sessions), populated by applyFileConfig from the [dream] table with
	// 中文: 最近的会话），由 [dream] 表中的 applyFileConfig 填充
	// EN: defaults applied. The interactive REPL consumes it to decide the startup
	// 中文: 应用默认值。交互式 REPL 使用它来决定启动
	// EN: background auto-consolidation (US-008). Like memory it has no CLI flags.
	// 中文: 后台自动合并（US-008）。与内存一样，它没有 CLI 标志。
	dreamCfg dream.Config
	// EN: allowedTools and disallowedTools are the --allowed-tools/--disallowed-tools
	// 中文: allowedTools 和 disallowedTools 是 --allowed-tools/--disallowed-tools
	// EN: values: the tool-level admission boundary for the run, filling the gap
	// 中文: 值：运行的工具级准入边界，填补空白
	// EN: between "all tools" and --no-tools. Each is repeatable and each value may be
	// 中文: 在“所有工具”和--无工具之间。每个都是可重复的，每个值都可以是
	// EN: comma-separated. Names match case-insensitively, and deny wins over allow
	// 中文: 以逗号分隔。名称匹配不区分大小写，拒绝胜于允许
	// EN: when a name appears on both sides (fail-closed). The boundary is enforced at
	// 中文: 当名字出现在两侧时（失败关闭）。边界强制执行于
	// EN: the tool-registration layer in run.SetupEnv, strictly before the
	// 中文: run.SetupEnv 中的工具注册层，严格位于
	// EN: BeforeToolCall confirmation gate, so --approve waives confirmation prompts
	// 中文: BeforeToolCall 确认门，因此 --approve 放弃确认提示
	// EN: but can never widen the boundary.
	// 中文: 但永远无法扩大边界。
	allowedTools    []string
	disallowedTools []string
}

func main() {
	// EN: Package-management subcommands (pigo install|list|uninstall|update ...) are
	// 中文: 包管理子命令（pigo install|list|uninstall|update ...）是
	// EN: positional and distinct from the flag-driven agent modes, so peel them off
	// 中文: 位置性的并且与标志驱动的代理模式不同，因此将它们剥离
	// EN: before pflag parsing — the agent flags don't apply to them.
	// 中文: 在 pflag 解析之前 - 代理标志不适用于它们。
	if len(os.Args) > 1 && pkgcmd.Subcommands[os.Args[1]] {
		// EN: `pigo update` routes by whether a positional package name follows it:
		// 中文: `pigo update` 根据位置包名称是否跟随它来进行路由：
		// EN: none — or flags-only, e.g. `pigo update --check` — is binary self-update
		// 中文: 无 - 或仅标志，例如`pigo update --check` — 是二进制自更新
		// EN: (#466: download the latest release and replace this binary); a package
		// 中文: （#466：下载最新版本并替换此二进制文件）；一个包裹
		// EN: name stays package-update (handled by pkgcmd). This is the US-003 dispatch
		// 中文: 名称保持 package-update （由 pkgcmd 处理）。这是US-003派遣
		// EN: split, with updateIsSelfUpdate as the pure classifier so routing is
		// 中文: split，使用 updateIsSelfUpdate 作为纯分类器，因此路由是
		// EN: unit-testable (TestUpdateIsSelfUpdate).
		// 中文: 可进行单元测试（TestUpdateIsSelfUpdate）。
		if os.Args[1] == "update" && updateIsSelfUpdate(os.Args[2:]) {
			os.Exit(selfupdate.Run(context.Background(), version, os.Stdout, os.Stderr))
		}
		os.Exit(pkgcmd.Run(os.Args[1], os.Args[2:], os.Stdout, os.Stderr))
	}

	var opts cliOptions
	flag.StringVarP(&opts.prompt, "print", "p", "", "prompt to run in headless print mode")
	flag.StringVarP(&opts.model, "model", "m", "openrouter/free", "model id to run against (a well-known model name like claude-opus-4-8 or deepseek-chat auto-selects its provider when --provider/--protocol/--base-url are unset)")
	flag.StringVarP(&opts.baseURL, "base-url", "u", "", "override provider base URL (e.g. local Ollama)")
	flag.StringVarP(&opts.apiKey, "api-key", "k", "", "API key for the resolved provider (overrides env/config; else <PROVIDER>_API_KEY)")
	flag.StringVarP(&opts.protocol, "protocol", "P", "", "force wire protocol for a custom endpoint: openai | anthropic (default: inferred from model id)")
	flag.StringVar(&opts.provider, "provider", "", "select a built-in provider by name (e.g. deepseek, minimax); uses its default base URL, protocol, and API-key env var (see --help provider list)")
	flag.StringVarP(&opts.outputFmt, "output-format", "o", "text", "output format: text | stream-json")
	flag.BoolVarP(&opts.noTools, "no-tools", "n", false, "disable the built-in file/shell tools")
	flag.StringArrayVar(&opts.allowedTools, "allowed-tools", nil, "restrict the model to these tools (repeatable, comma-separated, case-insensitive); empty means no restriction and --disallowed-tools wins on conflict")
	flag.StringArrayVar(&opts.disallowedTools, "disallowed-tools", nil, "remove these tools from the model's set (repeatable, comma-separated, case-insensitive); takes precedence over --allowed-tools")
	flag.BoolVarP(&opts.listSessions, "list-sessions", "l", false, "list stored interactive sessions and exit")
	flag.StringVarP(&opts.resumeID, "resume", "r", "", "resume the interactive session with this id")
	flag.BoolVarP(&opts.continueLast, "continue", "c", false, "resume the most recent interactive session")
	flag.BoolVarP(&opts.approve, "approve", "a", false, "trust the working directory for this run: skip the first-launch trust prompt and run side-effect tools without per-call confirmation")
	flag.BoolVar(&opts.noSkills, "no-skills", false, "disable skill discovery (do not load skills under ~/.agents/skills as /skill-name commands)")
	flag.BoolVar(&opts.noPromptTemplates, "no-prompt-templates", false, "disable prompt-template discovery (do not load ~/.pigo/{commands,prompts}, .pigo/prompts, config prompts, or --prompt-template); built-in slash commands are unaffected")
	flag.StringVar(&opts.systemPrompt, "system-prompt", "", "system prompt to use instead of the default coding-assistant prompt (mirrors pi --system-prompt)")
	flag.StringArrayVar(&opts.appendSystemPrompt, "append-system-prompt", nil, "append text or file contents to the system prompt; repeatable (mirrors pi --append-system-prompt)")
	flag.StringArrayVar(&opts.promptTemplates, "prompt-template", nil, "load a prompt template from a file or directory (non-recursive); repeatable (mirrors pi --prompt-template)")
	flag.StringVar(&opts.thinkingLevel, "thinking-level", "", "reasoning effort: off|minimal|low|medium|high|xhigh|max (overrides PIGO_THINKING_LEVEL and config; default medium)")
	flag.BoolVar(&opts.subagentRPC, "subagent-rpc", false, "internal: run as a process-isolated sub-agent JSON-RPC server over stdio (US-019)")
	flag.BoolVar(&opts.dream, "dream", false, "internal: run a memory-consolidation pass over the global/project memory scope, emit a Report JSON on stdout, and exit (SPEC §4.1)")
	flag.BoolVar(&opts.dreamDryRun, "dream-dry-run", false, "internal: with --dream, analyze and report without writing files or updating dream state (SPEC §5.5)")
	flag.BoolVar(&opts.noTUI, "no-tui", false, "use the line-based REPL instead of the full-screen TUI")
	flag.StringVarP(&opts.cwd, "cwd", "C", "", "run as if pigo was started in this directory (matches the Claude Agent SDK's cwd; like git -C): tool file access, trust, hooks, and project config all resolve against it")
	flag.BoolVarP(&opts.showVersion, "version", "v", false, "print version information and exit")
	// EN: Extend the default pflag usage with a "Supported providers" block so
	// 中文: 使用“支持的提供程序”块扩展默认 pflag 的使用，以便
	// EN: `--help` documents the values accepted by --provider (name → env var →
	// 中文: `--help` 记录了 --provider 接受的值（名称→环境变量→
	// EN: default base URL → protocol). The list is derived from the provider
	// 中文: 默认基本 URL → 协议）。该列表来自提供商
	// EN: registry, so it never drifts from the code.
	// 中文: 注册表，因此它永远不会偏离代码。
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		cli.PrintProviderHelp(out)
	}
	flag.Parse()

	// EN: --cwd switches the process working directory before anything cwd-derived is
	// 中文: --cwd 在 cwd 派生的任何内容之前切换进程工作目录
	// EN: resolved (tool roots, trust, hooks, project config, git info). Doing it here
	// 中文: 已解决（工具根、信任、挂钩、项目配置、git 信息）。在这里做
	// EN: — after parse, before config overlay and dispatch — means every downstream
	// 中文: — 解析之后，配置覆盖和分派之前 — 表示每个下游
	// EN: os.Getwd() sees the requested directory, so pigo behaves as if it had been
	// 中文: os.Getwd() 看到请求的目录，因此pigo 的行为就好像它已经被
	// EN: launched there. A bad path is a usage error (exit 2) rather than a silent
	// 中文: 在那里推出。错误的路径是使用错误（出口 2）而不是静默
	// EN: fall-through to the original directory.
	// 中文: 直接跳转到原始目录。
	if opts.cwd != "" {
		if err := os.Chdir(opts.cwd); err != nil {
			fmt.Fprintf(os.Stderr, "pigo: --cwd: %v\n", err)
			os.Exit(2)
		}
	}

	// EN: Overlay ~/.config/pigo/config.toml: file values replace built-in defaults,
	// 中文: 覆盖 ~/.config/pigo/config.toml：文件值替换内置默认值，
	// EN: but any flag the user set on the command line still wins (CLI > file >
	// 中文: 但用户在命令行上设置的任何标志仍然获胜（CLI > file >
	// EN: default). A malformed file warns but does not abort — defaults apply.
	// 中文: 默认）。格式错误的文件会发出警告，但不会中止 — 应用默认值。
	if cfg, err := config.LoadFileConfig(config.FileConfigPath()); err != nil {
		fmt.Fprintf(os.Stderr, "pigo: %v\n", err)
	} else {
		applyFileConfig(&opts, cfg, flag.CommandLine.Changed)
	}

	// EN: --version is a standalone action: print build metadata and exit.
	// 中文: --version 是一个独立的操作：打印构建元数据并退出。
	if opts.showVersion {
		fmt.Printf("pigo %s (commit %s, built %s)\n", version, commit, date)
		os.Exit(0)
	}

	// EN: A prompt may also be supplied as positional args.
	// 中文: 提示也可以作为位置参数提供。
	if opts.prompt == "" {
		opts.prompt = strings.TrimSpace(strings.Join(flag.Args(), " "))
	}

	os.Exit(dispatch(context.Background(), opts, os.Stdout, os.Stderr))
}

// EN: applyFileConfig overlays config.toml values onto opts, but only for flags the
// 中文: applyFileConfig 将 config.toml 值覆盖到 opts 上，但仅限于标记
// EN: user did not set on the command line (changed reports whether a flag name was
// 中文: 用户没有在命令行上设置（更改报告标志名称是否为
// EN: explicitly passed). This yields the precedence: CLI flag > config file >
// 中文: 明确通过）。这会产生优先级：CLI 标志 > 配置文件 >
// EN: default. Zero-valued config fields never override.
// 中文: 默认。零值配置字段永远不会覆盖。
func applyFileConfig(opts *cliOptions, cfg config.FileConfig, changed func(string) bool) {
	if cfg.Model != "" && !changed("model") {
		opts.model = cfg.Model
	}
	if cfg.BaseURL != "" && !changed("base-url") {
		opts.baseURL = cfg.BaseURL
	}
	if cfg.APIKey != "" && !changed("api-key") {
		opts.apiKey = cfg.APIKey
	}
	if cfg.Protocol != "" && !changed("protocol") {
		opts.protocol = cfg.Protocol
	}
	if cfg.Provider != "" && !changed("provider") {
		opts.provider = cfg.Provider
	}
	if cfg.ThinkingLevel != "" && !changed("thinking-level") {
		opts.thinkingLevel = cfg.ThinkingLevel
	}
	if cfg.OutputFormat != "" && !changed("output-format") {
		opts.outputFmt = cfg.OutputFormat
	}
	if cfg.NoTools && !changed("no-tools") {
		opts.noTools = true
	}
	if cfg.NoSkills && !changed("no-skills") {
		opts.noSkills = true
	}
	if cfg.Approve && !changed("approve") {
		opts.approve = true
	}
	if cfg.SystemPrompt != "" && !changed("system-prompt") {
		opts.systemPrompt = cfg.SystemPrompt
	}
	// EN: The tool boundary follows the standard precedence (CLI > file > default)
	// 中文: 工具边界遵循标准优先级（CLI > 文件 > 默认）
	// EN: rather than the additive treatment prompts get below. Merging would be the
	// 中文: 而不是下面提示的附加治疗。合并将是
	// EN: wrong semantics for a security boundary: a user passing --allowed-tools to
	// 中文: 安全边界的错误语义：用户传递 --allowed-tools 到
	// EN: widen what the file's allowed_tools narrowed must actually get the wider
	// 中文: 扩大文件的 allowed_tools 缩小范围实际上必须变得更宽
	// EN: set, not the intersection. Each flag overrides its own key independently:
	// 中文: 集，而不是交集。每个标志独立地覆盖自己的键：
	// EN: --allowed-tools does not clear a file-level disallowed_tools, and because
	// 中文: --allowed-tools 不会清除文件级别的 disallowed_tools，并且因为
	// EN: deny wins on conflict a file deny survives a CLI allow — re-admitting a
	// 中文: 拒绝在冲突时获胜 文件拒绝在 CLI 允许中幸存 - 重新承认
	// EN: file-denied tool requires overriding --disallowed-tools on the CLI.
	// 中文: file-denied 工具需要在 CLI 上覆盖 --disallowed-tools。
	if len(cfg.AllowedTools) > 0 && !changed("allowed-tools") {
		opts.allowedTools = cfg.AllowedTools
	}
	if len(cfg.DisallowedTools) > 0 && !changed("disallowed-tools") {
		opts.disallowedTools = cfg.DisallowedTools
	}
	// EN: prompts (settings tier) are additive with --prompt-template (CLI tier,
	// 中文: 提示（设置层）与 --prompt-template（CLI 层，
	// EN: wired in #339), so they are always passed through when present.
	// 中文: 在 #339 中连线），因此它们在存在时总是会被传递。
	if len(cfg.Prompts) > 0 {
		opts.configPrompts = cfg.Prompts
	}
	// EN: The [memory]/[checkpoint]/[compaction] tables have no CLI flags, so they
	// 中文: [内存]/[检查点]/[压缩]表没有 CLI 标志，因此它们
	// EN: are resolved (with defaults) and overlaid unconditionally — an absent set
	// 中文: 被解析（使用默认值）并无条件覆盖——一个缺席的集合
	// EN: of tables yields the default-safe MemorySettings.
	// 中文: 表的结果是默认安全的 MemorySettings。
	opts.memory = cfg.ResolveMemorySettings()
	// EN: The [dream] table also has no CLI flags; normalize it (defaults applied when
	// 中文: [dream] 表也没有 CLI 标志；对其进行标准化（默认值适用于
	// EN: the table is absent) so the interactive startup trigger has a resolved
	// 中文: 该表不存在），因此交互式启动触发器已解决
	// EN: Config. NewConfig treats a nil enabled as true, so dream is on by default.
	// 中文: 配置。 NewConfig 将 nil 启用视为 true，因此默认情况下 dream 处于启用状态。
	opts.dreamCfg = dream.NewConfig(cfg.Dream.Enabled, cfg.Dream.IntervalDays, cfg.Dream.RecentSessions)
}

// EN: dispatch runs the resolved command and returns a process exit code, writing
// 中文: 调度运行已解析的命令并返回进程退出代码，写入
// EN: diagnostics to errOut. It is the run-assembly seam: every path (list, REPL,
// 中文: 诊断错误。这是运行装配接缝：每个路径（列表、REPL、
// EN: headless, subagent-rpc) is reached from here, so the CLI's behavior can be
// 中文: headless, subagent-rpc) 是从这里到达的，所以 CLI 的行为可以是
// EN: exercised without re-parsing flags. A returned code of 0 is success.
// 中文: 无需重新解析标志即可执行。返回码0表示成功。
func dispatch(ctx context.Context, opts cliOptions, out, errOut io.Writer) int {
	// EN: --subagent-rpc is a fully separate mode: speak the sub-agent JSON-RPC
	// 中文: --subagent-rpc 是完全独立的模式：讲子代理 JSON-RPC
	// EN: protocol over stdio and exit. It is the subprocess end of process-isolated
	// 中文: 通过 stdio 的协议并退出。它是进程隔离的子进程结束
	// EN: sub-agents and shares nothing with the interactive/headless paths.
	// 中文: 子代理，并且与交互式/无头路径不共享任何内容。
	if opts.subagentRPC {
		return headless.RunSubAgentRPC(ctx, os.Stdin, out, errOut)
	}

	// EN: --dream is the subprocess consolidation mode (SPEC §4.1/§4.2): run one
	// 中文: --dream 是子进程整合模式（SPEC §4.1/§4.2）：运行一个
	// EN: memory-consolidation pass to completion, emit a single-line Report JSON on
	// 中文: 内存整合传递至完成，发出单行 JSON 报告
	// EN: stdout (progress/logs go to stderr), and exit 0 on success / 1 on failure.
	// 中文: stdout（进度/日志转到 stderr），成功时退出 0，失败时退出 1。
	// EN: It runs before any interactive/headless session assembly and honors -C/--cwd
	// 中文: 它在任何交互式/无头会话程序集之前运行并遵循 -C/--cwd
	// EN: for the project scope (applied above via os.Chdir). It shares nothing with
	// 中文: 对于项目范围（上面通过 os.Chdir 应用）。它与以下内容没有任何共享
	// EN: the REPL/headless paths.
	// 中文: REPL/无头路径。
	if opts.dream {
		return runDream(ctx, opts, out, errOut)
	}

	// EN: --list-sessions is a standalone action: print and exit.
	// 中文: --list-sessions 是一个独立的操作：打印并退出。
	if opts.listSessions {
		if err := headless.PrintSessions(out); err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 1
		}
		return 0
	}

	// EN: --continue resolves to the most recently updated session id.
	// 中文: --Continue 解析为最近更新的会话 ID。
	resumeID := opts.resumeID
	if opts.continueLast && resumeID == "" {
		id, err := headless.MostRecentSessionID()
		if err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 1
		}
		if id == "" {
			fmt.Fprintln(errOut, "pigo: no sessions to continue")
			return 1
		}
		resumeID = id
	}

	// EN: No prompt + an interactive terminal → start the interactive UI. By default
	// 中文: 无提示+交互式终端→启动交互式UI。默认情况下
	// EN: this is the full-screen TUI (US-001); --no-tui (or a non-terminal stdout)
	// 中文: 这是全屏 TUI (US-001)； --no-tui （或非终端标准输出）
	// EN: forces the line-based REPL (US-003). A --resume id also enters the
	// 中文: 强制基于行的 REPL (US-003)。 --resume id 也输入
	// EN: interactive UI to continue an existing session. No prompt with a
	// 中文: 用于继续现有会话的交互式 UI。没有提示
	// EN: non-terminal stdout (pipe/CI) and no resume is an error, since there is
	// 中文: 非终端标准输出（管道/CI）并且没有恢复是一个错误，因为有
	// EN: nothing to run and nothing to interact with.
	// 中文: 没有什么可以运行，也没有什么可以交互。
	if opts.prompt == "" {
		isTTY := ui.StdoutIsTerminal()
		if resumeID == "" && !isTTY {
			fmt.Fprintln(errOut, "pigo: no prompt (use -p \"...\" or positional args)")
			return 2
		}
		env, err := run.SetupEnv(opts.model, opts.baseURL, opts.protocol, opts.provider, opts.apiKey, opts.noTools, opts.noSkills, opts.systemPrompt, opts.appendSystemPrompt, opts.memory.Memory.Enabled, run.NewToolPolicy(opts.allowedTools, opts.disallowedTools))
		if err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return setupExitCode(err)
		}
		if env.Plugins != nil {
			defer env.Plugins.Close()
		}
		if env.Memory != nil {
			defer env.Memory.Close()
		}
		thinking, err := run.ResolveThinkingLevel(opts.thinkingLevel)
		if err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 2
		}
		if shouldUseTUI(opts, isTTY) {
			// EN: Refresh the cached latest-release check off the hot path so the banner
			// 中文: 刷新缓存的最新版本检查热路径，以便横幅
			// EN: can show an upgrade hint on this or the next launch without blocking
			// 中文: 可以在本次或下次启动时显示升级提示，而不会阻塞
			// EN: startup (US-004). No-ops for dev builds or a fresh cache.
			// 中文: 启动（US-004）。开发构建或新缓存无需执行任何操作。
			selfupdate.StartBackgroundCheck(version)
			if err := tui.Run(tui.Options{
				Model:             opts.model,
				ProviderName:      env.ProviderName,
				Provider:          env.Provider,
				BaseURL:           opts.baseURL,
				APIKey:            opts.apiKey,
				Protocol:          opts.protocol,
				Version:           version,
				ThinkingLevel:     thinking,
				Tools:             env.Tools,
				SysPrompt:         env.SysPrompt,
				ResumeID:          resumeID,
				Approve:           opts.approve,
				Skills:            env.Skills,
				Plugins:           env.Plugins,
				ConfigPrompts:     opts.configPrompts,
				CliPrompts:        opts.promptTemplates,
				NoPromptTemplates: opts.noPromptTemplates,
			}); err != nil {
				fmt.Fprintf(errOut, "pigo: %v\n", err)
				return 1
			}
			return 0
		}
		if err := repl.Run(repl.Options{
			Model:             opts.model,
			ProviderName:      env.ProviderName,
			Provider:          env.Provider,
			BaseURL:           opts.baseURL,
			APIKey:            opts.apiKey,
			Protocol:          opts.protocol,
			ThinkingLevel:     thinking,
			Tools:             env.Tools,
			SysPrompt:         env.SysPrompt,
			ResumeID:          resumeID,
			Approve:           opts.approve,
			Skills:            env.Skills,
			Plugins:           env.Plugins,
			ConfigPrompts:     opts.configPrompts,
			CliPrompts:        opts.promptTemplates,
			NoPromptTemplates: opts.noPromptTemplates,
			Dream:             opts.dreamCfg,
		}); err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 1
		}
		return 0
	}

	mode, err := headless.ParseOutputMode(opts.outputFmt)
	if err != nil {
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return 2
	}

	env, err := run.SetupEnv(opts.model, opts.baseURL, opts.protocol, opts.provider, opts.apiKey, opts.noTools, opts.noSkills, opts.systemPrompt, opts.appendSystemPrompt, opts.memory.Memory.Enabled, run.NewToolPolicy(opts.allowedTools, opts.disallowedTools))
	if err != nil {
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return setupExitCode(err)
	}
	if env.Plugins != nil {
		defer env.Plugins.Close()
	}
	if env.Memory != nil {
		defer env.Memory.Close()
	}
	return headless.Run(ctx, headless.RunParams{
		Mode:          mode,
		Env:           env,
		Prompt:        opts.prompt,
		Model:         opts.model,
		APIKey:        opts.apiKey,
		ThinkingLevel: opts.thinkingLevel,
		ResumeID:      resumeID,
	}, out, errOut)
}

// EN: setupExitCode maps a run.SetupEnv failure to a process exit code. A bad tool
// 中文: setupExitCode 将 run.SetupEnv 失败映射到进程退出代码。一个糟糕的工具
// EN: policy is a usage error (2), matching --cwd and --output-format; everything
// 中文: 策略是一个使用错误（2），匹配--cwd和--output-format；一切
// EN: else — provider resolution, prompt assembly — is a runtime failure (1).
// 中文: else — 提供者解析、提示组装 — 是运行时失败 (1)。
func setupExitCode(err error) int {
	var policyErr *run.ToolPolicyError
	if errors.As(err, &policyErr) {
		return 2
	}
	return 1
}

// EN: runDream executes the subprocess memory-consolidation pass (SPEC §4.1/§4.2).
// 中文: runDream 执行子进程内存整合过程（SPEC §4.1/§4.2）。
// EN: It runs dream.Runner to completion, marshals the resulting Report as a single
// 中文: 它运行 dream.Runner 直至完成，将生成的报告编组为单个报告
// EN: line of JSON on stdout (the parent/scheduler parses this), and returns the
// 中文: stdout 上的 JSON 行（父级/调度程序解析此），并返回
// EN: process exit code: 0 on success (including a "skipped" run when another dream
// 中文: 进程退出代码：成功时为 0（包括当另一个梦想时“跳过”运行）
// EN: holds the lock) or 1 on failure. Progress and diagnostics go to errOut. The
// 中文: 保持锁定）或失败时为 1。进度和诊断将转至 errOut。这
// EN: project scope comes from the working directory, which -C/--cwd already applied
// 中文: 项目范围来自工作目录，已应用 -C/--cwd
// EN: via os.Chdir before dispatch, so an empty ProjectDir here resolves to cwd.
// 中文: 在调度之前通过 os.Chdir ，因此这里的空 ProjectDir 解析为 cwd 。
func runDream(ctx context.Context, opts cliOptions, out, errOut io.Writer) int {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(errOut, "pigo: dream: %v\n", err)
		return 1
	}
	// EN: The dream pass reuses the main-session model (SPEC Q3): resolve the same
	// 中文: 梦想通行证重用主会话模型（SPEC Q3）：解决相同问题
	// EN: model/provider/api-key tuple cmd/pigo already overlaid from flags+config,
	// 中文: model/provider/api-key 元组 cmd/pigo 已从 flags+config 覆盖，
	// EN: and inject a real LLM-backed Consolidator so `pigo --dream` performs the
	// 中文: 并注入一个真正的 LLM 支持的 Consolidator，以便“pigo --dream”执行
	// EN: semantic merge/prune step (not just the deterministic dedup/path-clean).
	// 中文: 语义合并/修剪步骤（不仅仅是确定性的重复数据删除/路径清理）。
	thinking, err := run.ResolveThinkingLevel(opts.thinkingLevel)
	if err != nil {
		fmt.Fprintf(errOut, "pigo: dream: %v\n", err)
		return 1
	}
	cons, err := dream.NewLLMConsolidator(opts.model, opts.baseURL, opts.protocol, opts.provider, opts.apiKey, thinking)
	if err != nil {
		fmt.Fprintf(errOut, "pigo: dream: %v\n", err)
		return 1
	}
	r := &dream.Runner{Consolidator: cons}
	report, err := r.Run(ctx, dream.RunOptions{
		DryRun:     opts.dreamDryRun,
		ProjectDir: projectDir,
	})
	if err != nil {
		fmt.Fprintf(errOut, "pigo: dream: %v\n", err)
		return 1
	}
	// EN: Single-line JSON on stdout is the stdout contract (SPEC §4.2). Encoder
	// 中文: stdout 上的单行 JSON 是 stdout 合约（SPEC §4.2）。编码器
	// EN: writes a trailing newline, keeping the report one line.
	// 中文: 写入尾随换行符，使报告保持一行。
	if err := json.NewEncoder(out).Encode(report); err != nil {
		fmt.Fprintf(errOut, "pigo: dream: encode report: %v\n", err)
		return 1
	}
	return 0
}

// EN: shouldUseTUI is the pure entry-gating predicate for the no-prompt path
// 中文: shouldUseTUI 是无提示路径的纯入口门控谓词
// EN: (US-001, SPEC 4.2/5.2): the full-screen TUI is used only when stdout is a TTY
// 中文: （US-001，SPEC 4.2/5.2）：仅当 stdout 是 TTY 时才使用全屏 TUI
// EN: and --no-tui was not set. --no-tui or a non-terminal stdout always forces the
// 中文: 并且 --no-tui 未设置。 --no-tui 或非终端标准输出总是强制
// EN: line-based REPL. Keeping the decision in a side-effect-free function lets the
// 中文: 基于行的 REPL。将决策保留在无副作用的函数中可以让
// EN: gating be unit-tested without a real terminal or spawning Bubble Tea (see
// 中文: 门控可以在没有真正终端或生成珍珠奶茶的情况下进行单元测试（请参阅
// EN: TestDispatchTUIGating); dispatch handles the non-TTY/no-resume usage error
// 中文: 测试调度TUIGating）；调度处理非 TTY/无恢复使用错误
// EN: before calling this, so it only decides TUI-vs-REPL for the interactive case.
// 中文: 在调用此函数之前，因此它仅决定交互式情况下的 TUI-vs-REPL。
func shouldUseTUI(opts cliOptions, isTTY bool) bool {
	return isTTY && !opts.noTUI
}

// EN: updateIsSelfUpdate classifies the arguments that follow `pigo update` (US-003)
// 中文: updateIsSelfUpdate 对“pigo update”之后的参数进行分类 (US-003)
// EN: to route between binary self-update and pkgmgr package-update. It returns true
// 中文: 在二进制自更新和 pkgmgr 包更新之间进行路由。它返回 true
// EN: — self-update — when no positional package name is present: any argument that
// 中文: — 自更新 — 当不存在位置包名称时：任何参数
// EN: does not begin with '-' is treated as a package name and routes to
// 中文: 不以“-”开头的被视为包名称并路由到
// EN: package-update, while flags-only invocations (e.g. `pigo update --check`) stay
// 中文: 包更新，而仅标志调用（例如“pigo update --check”）保留
// EN: on the self-update path. Keeping the decision side-effect-free lets the routing
// 中文: 在自我更新的道路上。保持决策无副作用，让路由
// EN: be unit-tested without spawning either update path (see TestUpdateIsSelfUpdate).
// 中文: 进行单元测试，而不产生任何更新路径（请参阅 TestUpdateIsSelfUpdate）。
func updateIsSelfUpdate(rest []string) bool {
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}
