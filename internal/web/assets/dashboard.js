(() => {
  "use strict";

  const mutationHeader = "X-Brainslug-Dashboard";

  const showError = (message) => {
    const main = document.querySelector("main");
    if (!main) return;
    let notice = main.querySelector(".notice-error");
    if (!notice) {
      notice = document.createElement("div");
      notice.className = "notice notice-error";
      notice.setAttribute("role", "alert");
      main.prepend(notice);
    }
    notice.textContent = message;
    notice.scrollIntoView({ block: "nearest" });
  };

  const responseError = async (response) => {
    const body = await response.text();
    if (response.headers.get("Content-Type")?.includes("text/html")) {
      const parsed = new DOMParser().parseFromString(body, "text/html");
      const notice = parsed.querySelector(".notice-error, [role='alert']");
      if (notice?.textContent?.trim()) return notice.textContent.trim();
    }
    const text = body.trim();
    return text && text.length <= 300 ? text : `Dashboard update failed (${response.status}).`;
  };

  const trustedNavigation = (value) => {
    const target = new URL(value, window.location.href);
    if (target.origin === window.location.origin) return target;
    if (target.protocol === "https:" && target.hostname === "auth.openai.com" && !target.port) return target;
    throw new Error("Dashboard returned an invalid navigation target.");
  };

  for (const form of document.querySelectorAll("form[method='post']")) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (form.dataset.submitting === "true") return;

      const submitter = event.submitter;
      const action = submitter?.formAction || form.action;
      const method = submitter?.formMethod || form.method || "post";
      const fields = new URLSearchParams();
      for (const [name, value] of new FormData(form)) {
        if (typeof value === "string") fields.append(name, value);
      }

      form.dataset.submitting = "true";
      if (submitter) {
        submitter.disabled = true;
        submitter.setAttribute("aria-busy", "true");
      }

      try {
        const response = await fetch(action, {
          method,
          body: fields,
          credentials: "same-origin",
          redirect: "error",
          headers: {
            Accept: "application/json",
            [mutationHeader]: "fetch",
          },
        });
        if (!response.ok) throw new Error(await responseError(response));
        const result = await response.json();
        window.location.assign(trustedNavigation(result.location).href);
      } catch (error) {
        showError(error instanceof Error ? error.message : "Dashboard update failed.");
        form.dataset.submitting = "false";
        if (submitter) {
          submitter.disabled = false;
          submitter.removeAttribute("aria-busy");
        }
      }
    });
  }
})();
