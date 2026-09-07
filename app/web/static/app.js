document.addEventListener("submit", (event) => {
  const message = event.target.dataset.confirm;
  if (message && !window.confirm(message)) event.preventDefault();
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-password-toggle]");
  if (!button) return;
  const input = button.closest(".password-field")?.querySelector("input");
  if (!input) return;
  const show = input.type === "password";
  input.type = show ? "text" : "password";
  button.setAttribute("aria-pressed", String(show));
  button.setAttribute("aria-label", show ? "隐藏密码" : "显示密码");
  input.focus();
});

document.addEventListener("DOMContentLoaded", () => {
  const toggle = document.querySelector(".sidebar-toggle");
  const backdrop = document.querySelector(".sidebar-backdrop");
  const closeSidebar = () => {
    document.body.classList.remove("sidebar-open");
    toggle?.setAttribute("aria-expanded", "false");
  };
  toggle?.addEventListener("click", () => {
    if (window.matchMedia("(max-width: 760px)").matches) {
      document.body.classList.toggle("sidebar-open");
      toggle.setAttribute(
        "aria-expanded",
        document.body.classList.contains("sidebar-open") ? "true" : "false",
      );
    } else {
      document.body.classList.toggle("sidebar-collapsed");
    }
  });
  backdrop?.addEventListener("click", closeSidebar);
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeSidebar();
  });

  const uptime = document.querySelector("#program-uptime[data-uptime-seconds]");
  if (uptime) {
    const initialSeconds = Math.max(0, Number(uptime.dataset.uptimeSeconds) || 0);
    const startedCountingAt = performance.now();
    const formatUptime = (value) => {
      const totalSeconds = Math.max(0, Math.floor(value));
      const days = Math.floor(totalSeconds / 86400);
      const hours = Math.floor((totalSeconds % 86400) / 3600);
      const minutes = Math.floor((totalSeconds % 3600) / 60);
      const seconds = totalSeconds % 60;
      const clock = [hours, minutes, seconds]
        .map((part) => String(part).padStart(2, "0"))
        .join(":");
      return days ? `${days}天 ${clock}` : clock;
    };
    const updateUptime = () => {
      const elapsed = (performance.now() - startedCountingAt) / 1000;
      uptime.textContent = formatUptime(initialSeconds + elapsed);
    };
    updateUptime();
    window.setInterval(updateUptime, 1000);
  }

  const output = document.querySelector("#log-output[data-stream]");
  if (!output) return;
  const streamUrl = new URL(output.dataset.stream, window.location.origin);
  streamUrl.searchParams.set("offset", output.dataset.offset || "0");
  const stream = new EventSource(streamUrl);
  stream.onmessage = (event) => {
    if (output.textContent === "暂无日志输出。") output.textContent = "";
    output.textContent = JSON.parse(event.data) + output.textContent;
    output.scrollTop = 0;
  };
  stream.addEventListener("done", () => stream.close());
  stream.onerror = () => stream.close();
});

(() => {
  if (document.body.dataset.authenticated !== "true") return;
  const context = document.modelContext;
  if (!context?.registerTool) return;
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";

  context.registerTool({
    name: "get_ctyun_auto_status",
    title: "读取 ctyun-auto 状态",
    description: "读取 CtYun 保活、账号数量和当前任务状态。",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
    annotations: { readOnlyHint: true, untrustedContentHint: false },
    async execute() {
      const response = await fetch("/api/status");
      if (!response.ok) throw new Error("无法读取运行状态");
      return response.json();
    },
  });

  context.registerTool({
    name: "run_ctyun_account_task",
    title: "运行账号任务",
    description: "为指定账号启动登录云电脑、AI 对话或云电脑挂机任务。",
    inputSchema: {
      type: "object",
      properties: {
        accountId: { type: "integer", minimum: 1 },
        taskType: { type: "string", enum: ["login", "chat", "pc"] },
      },
      required: ["accountId", "taskType"],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: false, untrustedContentHint: false },
    async execute(input) {
      if (!Number.isInteger(input.accountId) || !["login", "chat", "pc"].includes(input.taskType)) {
        throw new Error("账号编号或任务类型无效");
      }
      const response = await fetch(`/api/accounts/${input.accountId}/tasks/${input.taskType}`, {
        method: "POST",
        headers: { "x-csrf-token": csrf },
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "任务启动失败");
      window.location.assign(`/logs?run_id=${result.run_id}`);
      return result;
    },
  });
})();
