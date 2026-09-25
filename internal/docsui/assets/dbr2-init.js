// SPDX-License-Identifier: Apache-2.0
// DBR² Swagger UI bootstrap. Kept as an external file so the page's CSP can
// use script-src 'self' without 'unsafe-inline' or hashes.
(function () {
  const cfg = JSON.parse(document.currentScript.dataset.config);
  window.addEventListener("load", function () {
    const { oauth2, ...uiConfig } = cfg;
    if (oauth2) {
      uiConfig.oauth2RedirectUrl =
        window.location.origin + window.location.pathname.replace(/\/$/, "") + "/oauth2-redirect.html";
    }
    window.ui = SwaggerUIBundle({
      ...uiConfig,
      dom_id: "#swagger-ui",
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout",
      // Browser session: same-origin fetches (spec + "Try it out") send the
      // session cookie automatically; force it explicitly for clarity.
      requestInterceptor: function (req) {
        req.credentials = "same-origin";
        return req;
      },
    });
    if (oauth2) {
      window.ui.initOAuth(oauth2);
    }
  });
})();
