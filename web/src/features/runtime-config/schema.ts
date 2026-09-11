import {
  CONFIG_KEYS as K,
  DEFAULTS as D,
  FORM_CONSTRAINTS as C,
  STRATEGIES,
  STICKY_MODES,
  AUTH_MODES,
  CONVERSATION_RECOVERY_POLICY_OPTIONS,
} from "../../config";
import type { ConfigCategory } from "./types";

export const CONFIG_CATEGORIES: readonly ConfigCategory[] = [
  {
    id: "routing",
    title: "路由与会话",
    description: "决定请求发往哪里，以及后续对话如何保持连续。",
    groups: [
      {
        title: "路由选择",
        description: "显式分组与独立供应商在根级别参与选路。",
        fields: [
          {
            key: K.ROOT_CANDIDATE_STRATEGY,
            label: "根级路由策略",
            kind: "choice",
            description: "选择可用路由目标的方式。",
            defaultValue: D.ROOT_CANDIDATE_STRATEGY,
            options: [
              {
                value: STRATEGIES.PRIORITY,
                label: "优先级",
                description: "优先使用排序靠前的目标，适合主备切换。",
              },
              {
                value: STRATEGIES.RANDOM,
                label: "随机",
                description: "从可用目标中随机选择，分散请求。",
              },
              {
                value: STRATEGIES.WEIGHT,
                label: "权重",
                description: "按权重分配请求，权重越高越容易选中。",
              },
            ],
          },
        ],
      },
      {
        title: "会话连续性",
        description: "粘性路由与跨账号恢复分别控制，互不替代。",
        fields: [
          {
            key: K.STICKY_MODE,
            label: "粘性路由",
            kind: "select",
            description: "将同一用户的后续请求绑定到相同供应商。",
            defaultValue: D.STICKY_MODE,
            options: [
              { value: STICKY_MODES.OFF, label: "关闭 · 每次独立选路" },
              { value: STICKY_MODES.API_TYPE, label: "按用户 + API 类型" },
              { value: STICKY_MODES.MODEL, label: "按用户 + API 类型 + 模型" },
            ],
          },
          {
            key: K.STICKY_TTL,
            label: "粘性有效期",
            kind: "number",
            unit: "秒",
            min: C.MIN_ZERO,
            description: "粘性绑定的保留时长；关闭粘性路由时不生效。",
            defaultValue: D.STICKY_TTL,
            disabledWhen: (values) =>
              values[K.STICKY_MODE] === STICKY_MODES.OFF,
          },
          {
            key: K.CONVERSATION_RECOVERY_POLICY,
            label: "对话恢复策略",
            kind: "select",
            description:
              "允许换账号时，按粘性和选路策略选择可用账号，原样传递对话状态。",
            defaultValue: D.CONVERSATION_RECOVERY_POLICY,
            options: CONVERSATION_RECOVERY_POLICY_OPTIONS,
            help: "切回固定原账号后，已跨账号续聊的对话可能无法继续。",
          },
          {
            key: K.WEBSOCKET_PROBE_CLIENT_MODEL,
            label: "WebSocket 模型探测",
            kind: "toggle",
            description:
              "握手缺少可用模型时，在选路前探测客户端模型。关闭后仅根据握手信息选路。",
            defaultValue: D.WEBSOCKET_PROBE_CLIENT_MODEL,
          },
        ],
      },
    ],
  },
  {
    id: "accounts",
    title: "认证与账号",
    description: "管理上游请求认证，以及 GPT 账号操作的客户端特征。",
    groups: [
      {
        title: "请求认证",
        description: "供应商的独立设置可覆盖全局认证模式。",
        fields: [
          {
            key: K.AUTH_MODE,
            label: "认证模式",
            kind: "select",
            description: "向上游传递凭据时使用的认证方式。",
            defaultValue: D.AUTH_MODE,
            options: [
              { value: AUTH_MODES.AUTO, label: "自动识别" },
              { value: AUTH_MODES.BEARER, label: "Bearer Token" },
              { value: AUTH_MODES.X_API_KEY, label: "X-API-Key" },
            ],
          },
          {
            key: K.USER_HEADER,
            label: "用户标识请求头",
            kind: "text",
            description: "从此请求头读取用户标识。",
            defaultValue: D.USER_HEADER,
          },
        ],
      },
      {
        title: "Codex OAuth 登录",
        description: "设置浏览器授权时使用的客户端来源标识。",
        fields: [
          {
            key: K.CODEX_OAUTH_ORIGINATOR,
            label: "OAuth originator",
            kind: "combobox",
            description: "选择常用客户端标识，也可直接输入自定义值。",
            defaultValue: D.CODEX_OAUTH_ORIGINATOR,
            options: [
              { value: D.CODEX_OAUTH_ORIGINATOR, label: "Codex CLI" },
              { value: "codex_vscode", label: "Codex VS Code" },
            ],
            help: "留空使用 codex_cli_rs。保存后从下一次登录或重新授权生效。",
          },
        ],
      },
      {
        title: "GPT 账号请求",
        description: "适用于 GPT 登录、Token 刷新和额度查询。",
        fields: [
          {
            key: K.GPT_ACCOUNT_FALLBACK_CLIENT,
            label: "无伪装 UA 时的客户端特征",
            kind: "select",
            description:
              "凭据绑定的伪装 UA 及版本规则始终优先；浏览器授权页面使用浏览器自身的 UA。",
            defaultValue: D.GPT_ACCOUNT_FALLBACK_CLIENT,
            options: [
              {
                value: D.GPT_ACCOUNT_FALLBACK_CLIENT,
                label: "保持当前行为（默认）",
                description:
                  "优先使用凭据绑定的客户端 UA；没有可用 UA 时使用 switch-a/版本。",
              },
              {
                value: "official_stable",
                label: "同步 Codex CLI 官方稳定版",
                description:
                  "无可用伪装 UA 时，使用内置 Codex Desktop 模板并同步 Codex CLI 官方稳定版；首次同步完成前沿用模板版本。",
              },
            ],
            help: "保存后从下一次账号操作生效。",
          },
        ],
      },
    ],
  },
  {
    id: "requests",
    title: "超时与重试",
    description: "设置请求的等待时长、尝试次数和故障恢复行为。",
    groups: [
      {
        title: "超时",
        description: "分别控制连接、首字节与响应空闲阶段。",
        fields: [
          {
            key: K.UPSTREAM_CONNECT_TIMEOUT,
            label: "连接超时",
            kind: "number",
            description: "建立上游连接的最长等待时间。",
            defaultValue: D.UPSTREAM_CONNECT_TIMEOUT,
            min: C.MIN_POSITIVE,
            unit: "秒",
          },
          {
            key: K.FIRST_BYTE_TIMEOUT,
            label: "首字节超时",
            kind: "number",
            description: "等待上游首字节的时长。0 表示不限制。",
            defaultValue: D.FIRST_BYTE_TIMEOUT,
            min: C.MIN_ZERO,
            unit: "秒",
          },
          {
            key: K.UPSTREAM_READ_TIMEOUT,
            label: "响应空闲超时",
            kind: "number",
            description: "响应开始后连续未收到数据的最长时长。0 表示不限制。",
            defaultValue: D.UPSTREAM_READ_TIMEOUT,
            min: C.MIN_ZERO,
            unit: "秒",
          },
          {
            key: K.SSE_IDLE_TIMEOUT,
            label: "SSE 空闲超时",
            kind: "number",
            description: "SSE 流连续未收到数据的最长时长。0 表示不限制。",
            defaultValue: D.SSE_IDLE_TIMEOUT,
            min: C.MIN_ZERO,
            unit: "秒",
          },
        ],
      },
      {
        title: "请求限制与重试",
        description: "控制单次请求的资源与尝试预算。",
        fields: [
          {
            key: K.GLOBAL_MAX_ATTEMPTS,
            label: "全局最大尝试次数",
            kind: "number",
            unit: "次",
            description:
              "整个请求尝试供应商的次数总和。0 表示不限，依次尝试可用供应商直到成功。",
            defaultValue: D.GLOBAL_MAX_ATTEMPTS,
            min: C.MIN_ZERO,
            max: C.MAX_GLOBAL_ATTEMPTS,
            help: "单个供应商的重试次数由其 max_retries 控制；默认 0 表示试一次就切换。",
          },
          {
            key: K.MAX_BODY_SIZE,
            label: "最大请求体",
            kind: "number",
            unit: "MB",
            description: "允许接收的请求体大小上限。",
            defaultValue: D.MAX_BODY_SIZE_MB,
            min: C.MIN_POSITIVE,
          },
        ],
      },
      {
        title: "熔断恢复",
        description: "暂时停用持续失败的供应商，等待恢复后再次尝试。",
        fields: [
          {
            key: K.CIRCUIT_FAILURE,
            label: "失败阈值",
            kind: "number",
            unit: "次",
            description: "检测窗口内触发熔断的失败次数。",
            defaultValue: D.CIRCUIT_FAILURE,
            min: C.MIN_POSITIVE,
          },
          {
            key: K.CIRCUIT_WINDOW,
            label: "检测窗口",
            kind: "number",
            unit: "秒",
            description: "累计供应商失败次数的时间窗口。",
            defaultValue: D.CIRCUIT_WINDOW,
            min: C.MIN_POSITIVE,
          },
          {
            key: K.CIRCUIT_DISABLE,
            label: "熔断停用时长",
            kind: "number",
            unit: "秒",
            description: "触发熔断后暂停使用该供应商的时长。",
            defaultValue: D.CIRCUIT_DISABLE,
            min: C.MIN_POSITIVE,
          },
        ],
      },
    ],
  },
  {
    id: "system",
    title: "代理与日志",
    description: "管理代理环境的请求来源识别与日志保留。",
    groups: [
      {
        title: "系统设置",
        description: "根据实际部署环境调整。",
        fields: [
          {
            key: K.TRUST_PROXY_HEADERS,
            label: "信任代理请求头",
            kind: "toggle",
            description:
              "使用 X-Forwarded-For 等请求头识别来源；部署在可信反向代理或负载均衡器后时启用。",
            defaultValue: D.TRUST_PROXY_HEADERS,
          },
          {
            key: K.LOG_RETENTION_DAYS,
            label: "日志保留时间",
            kind: "number",
            unit: "天",
            description: "超过保留时间的请求日志会被清理。",
            defaultValue: D.LOG_RETENTION_DAYS,
            min: C.MIN_POSITIVE,
          },
        ],
      },
    ],
  },
];

export const CONFIG_FIELDS = CONFIG_CATEGORIES.flatMap((category) =>
  category.groups.flatMap((group) => group.fields),
);
