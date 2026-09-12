(() => {
  let theme = "";
  try {
    theme = localStorage.getItem("ctyun-theme") || "";
  } catch (_) {
    // Fall back to the operating-system preference when storage is unavailable.
  }
  if (theme !== "light" && theme !== "dark") {
    theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  document.documentElement.dataset.theme = theme;
})();
