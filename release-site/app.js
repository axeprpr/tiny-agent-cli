const DOWNLOADS = [
  { name: "macOS Apple Silicon", platform: "macOS", arch: "arm64", asset: "tacli-darwin-arm64" },
  { name: "macOS Intel", platform: "macOS", arch: "amd64", asset: "tacli-darwin-amd64" },
  { name: "Linux amd64", platform: "Linux", arch: "amd64", asset: "tacli-linux-amd64" },
  { name: "Linux arm64", platform: "Linux", arch: "arm64", asset: "tacli-linux-arm64" },
  { name: "Windows amd64", platform: "Windows", arch: "amd64", asset: "tacli-windows-amd64.exe" },
  { name: "Windows arm64", platform: "Windows", arch: "arm64", asset: "tacli-windows-arm64.exe" },
];

const copy = {
  en: {
    intro: "Minimal terminal coding agent. Download the latest binary for your platform.",
    featuresTitle: "Features",
    releaseNotes: "Release Notes",
    latestVersionLabel: "Latest Version",
    updatedLabel: "Updated",
    routeLabel: "Download Source",
    installTitle: "Install",
    installIntro: "Pick the matching binary below. If you are in mainland China, switch to OSS.",
    routeGlobal: "Global",
    routeChina: "China Mainland",
    installCommandLabel: "Quick install",
    downloadsTitle: "Downloads",
    features: [
      "One small binary. No Node.js, no Python runtime, no background service.",
      "Interactive chat mode with streaming output.",
      "Background jobs, persistent memory, shell and file tools.",
      "Confirmation by default, with dangerously mode when you want speed.",
    ],
    routePanelGlobal: "Using GitHub Releases.",
    routePanelChinaReady: "Using Aliyun OSS.",
    routePanelChinaMissing: "OSS is not configured. Falling back to GitHub Releases.",
    routeGitHubValue: "GitHub Releases",
    routeOssValue: "Aliyun OSS",
    downloadButtonGitHub: "Download",
    downloadButtonOss: "OSS Download",
    publishedFallback: "Unknown",
    loadingVersion: "Loading",
    loadingUpdated: "Waiting",
  },
  zh: {
    intro: "极简终端编码代理。直接下载适合你平台的最新版二进制。",
    featuresTitle: "功能简介",
    releaseNotes: "发行说明",
    latestVersionLabel: "最新版本",
    updatedLabel: "更新时间",
    routeLabel: "下载源",
    installTitle: "安装",
    installIntro: "选择对应平台的二进制。如果你在中国大陆，切到 OSS 即可。",
    routeGlobal: "全球",
    routeChina: "中国大陆",
    installCommandLabel: "快速安装",
    downloadsTitle: "下载地址",
    features: [
      "单文件二进制。不需要 Node.js、不需要 Python 运行时、不需要后台服务。",
      "支持交互式 chat，会实时流式输出。",
      "支持后台任务、持久记忆、shell 和文件工具。",
      "默认安全确认，需要速度时可切到 dangerously 模式。",
    ],
    routePanelGlobal: "当前使用 GitHub Releases。",
    routePanelChinaReady: "当前使用阿里云 OSS。",
    routePanelChinaMissing: "OSS 未配置，当前回退到 GitHub Releases。",
    routeGitHubValue: "GitHub Releases",
    routeOssValue: "阿里云 OSS",
    downloadButtonGitHub: "下载",
    downloadButtonOss: "OSS 下载",
    publishedFallback: "未知",
    loadingVersion: "加载中",
    loadingUpdated: "等待中",
  },
};

const state = {
  language: /^zh/i.test(navigator.language || "") ? "zh" : "en",
  route: /^zh/i.test(navigator.language || "") ? "china" : "global",
  release: null,
  config: null,
};

async function main() {
  bindToggles();
  await loadConfig();
  render();
}

function bindToggles() {
  document.getElementById("language-toggle").addEventListener("click", (event) => {
    const button = event.target.closest("[data-language]");
    if (!button) return;
    state.language = button.dataset.language === "zh-CN" ? "zh" : "en";
    render();
  });

  document.getElementById("route-toggle").addEventListener("click", (event) => {
    const button = event.target.closest("[data-route]");
    if (!button) return;
    state.route = button.dataset.route;
    render();
  });
}

async function loadConfig() {
  try {
    const response = await fetch("/api/config");
    if (!response.ok) throw new Error("config fetch failed");
    const data = await response.json();
    state.config = data;
    state.release = data.release || null;
  } catch {
    state.config = { owner: "axeprpr", repo: "tiny-agent-cli", ossMirrorBase: "", release: null };
    state.release = null;
  }
}

function render() {
  const lang = copy[state.language];
  const config = state.config || {};
  const release = state.release;
  const tag = release?.tag_name || lang.loadingVersion;
  const notesUrl =
    release?.html_url || `https://github.com/${config.owner || "axeprpr"}/${config.repo || "tiny-agent-cli"}/releases`;
  const publishedAt = release?.published_at
    ? new Date(release.published_at).toLocaleString(state.language === "zh" ? "zh-CN" : "en-US", {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : lang.publishedFallback;
  const ossReady = Boolean(config.ossMirrorBase);
  const usingOss = state.route === "china" && ossReady;

  document.documentElement.lang = state.language === "zh" ? "zh-CN" : "en";
  document.querySelectorAll("[data-i18n]").forEach((node) => {
    const key = node.dataset.i18n;
    if (lang[key]) node.textContent = lang[key];
  });
  document.getElementById("latest-version").textContent = tag;
  document.getElementById("latest-published-at").textContent = publishedAt;
  document.getElementById("active-route-label").textContent = usingOss ? lang.routeOssValue : lang.routeGitHubValue;
  document.getElementById("release-notes-link").href = notesUrl;
  document.getElementById("route-panel").textContent =
    state.route === "china"
      ? ossReady
        ? lang.routePanelChinaReady
        : lang.routePanelChinaMissing
      : lang.routePanelGlobal;

  renderFeatures(lang);
  renderInstallCommand(usingOss, config);
  renderDownloads(usingOss, config);
  setActive("language-toggle", state.language === "zh" ? "zh-CN" : "en", "data-language");
  setActive("route-toggle", state.route, "data-route");
}

function renderFeatures(lang) {
  const list = document.getElementById("feature-list");
  list.innerHTML = "";
  for (const item of lang.features) {
    const li = document.createElement("li");
    li.textContent = item;
    list.appendChild(li);
  }
}

function renderInstallCommand(usingOss, config) {
  const base = usingOss
    ? `${config.ossMirrorBase.replace(/\/$/, "")}/latest`
    : `https://github.com/${config.owner || "axeprpr"}/${config.repo || "tiny-agent-cli"}/releases/latest/download`;
  document.getElementById("install-command").textContent =
    `curl -fsSL ${base}/tacli-linux-amd64 -o tacli && chmod +x tacli`;
}

function renderDownloads(usingOss, config) {
  const lang = copy[state.language];
  const list = document.getElementById("download-cards");
  const template = document.getElementById("card-template");
  const githubBase = `https://github.com/${config.owner || "axeprpr"}/${config.repo || "tiny-agent-cli"}/releases/latest/download`;
  const ossBase = `${(config.ossMirrorBase || "").replace(/\/$/, "")}/latest`;

  list.innerHTML = "";
  for (const item of DOWNLOADS) {
    const node = template.content.firstElementChild.cloneNode(true);
    const url = usingOss ? `${ossBase}/${item.asset}` : `${githubBase}/${item.asset}`;
    node.querySelector(".download-name").textContent = item.name;
    node.querySelector(".download-meta").textContent = `${item.platform} · ${item.arch} · ${item.asset}`;
    const link = node.querySelector(".download-link");
    link.href = url;
    link.textContent = usingOss ? lang.downloadButtonOss : lang.downloadButtonGitHub;
    list.appendChild(node);
  }
}

function setActive(containerId, value, attr) {
  document.querySelectorAll(`#${containerId} [${attr}]`).forEach((node) => {
    node.classList.toggle("is-active", node.getAttribute(attr) === value);
  });
}

main().catch(() => {
  document.getElementById("latest-version").textContent = "Unavailable";
  document.getElementById("latest-published-at").textContent = "Unavailable";
});
