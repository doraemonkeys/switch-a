interface ClientTypePresentation {
  name: string;
  description: string;
  originator: string;
}

// Use the same entry-point names for outgoing profiles and observed clients.
export const CLIENT_TYPES: Record<string, ClientTypePresentation | undefined> =
  {
    desktop: {
      name: "Codex Desktop",
      description: "Codex 桌面应用。",
      originator: "Codex Desktop",
    },
    tui: {
      name: "Codex CLI（交互式 TUI）",
      description: "运行 codex，在终端中交互聊天。",
      originator: "codex-tui",
    },
    exec: {
      name: "Codex CLI（非交互 exec）",
      description: "运行 codex exec，用于脚本和自动化任务。",
      originator: "codex_exec",
    },
    cli: {
      name: "Codex 默认入口标识（未指定入口）",
      description:
        "底层客户端未指定具体入口时的默认标识；终端交互请选择 TUI，脚本执行请选择 exec。",
      originator: "codex_cli_rs",
    },
  };
