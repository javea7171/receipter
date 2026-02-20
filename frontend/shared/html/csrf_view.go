package html

// CSRFFormScript injects a hidden _csrf field into POST forms based on the CSRF cookie.
func CSRFFormScript() string {
	return `<script>
(function () {
  function getCookie(name) {
    var prefix = name + "=";
    var parts = document.cookie ? document.cookie.split(";") : [];
    for (var i = 0; i < parts.length; i++) {
      var c = parts[i].trim();
      if (c.indexOf(prefix) === 0) return decodeURIComponent(c.substring(prefix.length));
    }
    return "";
  }

  function inject() {
    var token = getCookie("X-CSRF-Token");
    if (!token) return;

    var forms = document.querySelectorAll("form");
    for (var i = 0; i < forms.length; i++) {
      var form = forms[i];
      var method = (form.getAttribute("method") || "GET").toUpperCase();
      if (method !== "POST") continue;
      if (form.querySelector("input[name='_csrf']")) continue;

      var input = document.createElement("input");
      input.type = "hidden";
      input.name = "_csrf";
      input.value = token;
      form.appendChild(input);
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", inject);
  } else {
    inject();
  }
})();
</script>`
}
