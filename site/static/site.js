// Somad website: keyboard shortcuts, the tmux-style status line, and the
// quick start's typing and copy button. Everything here is decoration; the
// page reads the same without it.
(function () {
  "use strict";

  var still = window.matchMedia("(prefers-reduced-motion: reduce)");

  // Single-key shortcuts, as in the TUI: links carrying data-key.
  function shortcuts() {
    var keys = {};
    document.querySelectorAll("a[data-key]").forEach(function (a) { keys[a.dataset.key] = a; });
    document.addEventListener("keydown", function (e) {
      if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return;
      var t = e.target;
      if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
      var a = keys[e.key];
      if (!a) return;
      e.preventDefault();
      a.click();
    });
  }

  // Copy the quick start's commands without their prompts. The text is
  // read up front, before the typing below empties the block.
  function copyButtons() {
    if (!navigator.clipboard) return;
    document.querySelectorAll(".copy").forEach(function (btn) {
      var code = btn.parentNode.querySelector("pre code");
      var text = code.textContent.split("\n").map(function (l) { return l.replace(/^\$\s*/, ""); }).join("\n");
      btn.hidden = false;
      btn.addEventListener("click", function () {
        navigator.clipboard.writeText(text).then(function () {
          btn.textContent = "copied";
          setTimeout(function () { btn.textContent = "copy"; }, 1600);
        });
      });
    });
  }

  // Type the quick start's commands out the first time they scroll into
  // view, one line after another behind a block cursor.
  function typeOut(code) {
    if (still.matches || !("IntersectionObserver" in window)) return;
    var lines = code.textContent.split("\n").map(function (l) { return l.replace(/^\$\s*/, ""); });
    var obs = new IntersectionObserver(function (e) {
      if (!e[0].isIntersecting) return;
      obs.disconnect();
      code.parentNode.style.minHeight = code.parentNode.offsetHeight + "px";
      code.textContent = "";
      var cursor = document.createElement("span");
      cursor.className = "cursor";
      var line = 0, col = 0, text = null;
      function step() {
        if (col === 0) {
          if (line > 0) code.insertBefore(document.createTextNode("\n"), cursor);
          var p = document.createElement("span");
          p.className = "p";
          p.textContent = "$";
          code.insertBefore(p, cursor);
          text = document.createTextNode(" ");
          code.insertBefore(text, cursor);
        }
        text.data += lines[line].charAt(col++);
        if (col < lines[line].length) return setTimeout(step, 28 + Math.random() * 40);
        line++; col = 0;
        if (line < lines.length) return setTimeout(step, 420);
        setTimeout(function () { cursor.remove(); }, 2400);
      }
      code.appendChild(cursor);
      setTimeout(step, 500);
    }, { threshold: 1 });
    obs.observe(code);
  }

  // The status line: flag the window whose section is on screen.
  function statusLine() {
    var links = document.querySelectorAll(".menu a[href^='#']");
    if ("IntersectionObserver" in window) {
      var shown = {};
      var obs = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) { shown[e.target.id] = e.isIntersecting; });
        links.forEach(function (a) { a.classList.toggle("current", !!shown[a.hash.slice(1)]); });
      }, { rootMargin: "-45% 0px -50% 0px" });
      links.forEach(function (a) {
        var target = document.getElementById(a.hash.slice(1));
        if (target) obs.observe(target);
      });
    }
  }

  shortcuts();
  statusLine();
  copyButtons();
  var shell = document.querySelector(".shell code");
  if (shell) typeOut(shell);
})();
