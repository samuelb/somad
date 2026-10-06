// Somad website: the hero's spectrum and tuning dial, keyboard shortcuts,
// and the quick start's copy button. Everything here is decoration; the
// page reads the same without it.
(function () {
  "use strict";

  var still = window.matchMedia("(prefers-reduced-motion: reduce)");

  // Spectrum: block-glyph bars like the TUI's visualizer, driven by a
  // made-up signal (a kick on the beat, a wandering melody, some noise),
  // rising fast and falling slowly the way cava does.
  function spectrum(pre) {
    var glyphs = " ▁▂▃▄▅▆▇█";
    var rows = 6, fps = 25, bpm = 112;
    var cols = 0, levels = [], seeds = [], timer = null, visible = true;

    function measure() {
      var probe = document.createElement("span");
      probe.textContent = "█".repeat(10);
      pre.textContent = "";
      pre.appendChild(probe);
      var w = probe.getBoundingClientRect().width / 10;
      pre.removeChild(probe);
      cols = Math.max(8, Math.floor(pre.clientWidth / (w || 8)));
      while (levels.length < cols) { levels.push(0); seeds.push(Math.random() * 1000); }
      levels.length = seeds.length = cols;
    }

    function frame(t) {
      var beat = (t / 60000 * bpm) % 1;
      var kick = Math.pow(1 - beat, 3);
      var max = rows * 8, out = [];
      for (var i = 0; i < cols; i++) {
        var x = i / cols;
        var s = seeds[i];
        var bass = Math.pow(1 - x, 3) * (0.3 + 0.7 * kick);
        var body = 0.6 * (0.5 + 0.5 * Math.sin(t / 700 + s)) * (0.5 + 0.5 * Math.sin(x * 7 - t / 1100));
        var air = 0.25 * Math.random();
        var target = Math.min(1, (0.9 * bass + body + air) * (1 - x * 0.35)) * max;
        levels[i] = target > levels[i] ? target : Math.max(target, levels[i] - max * 0.06);
      }
      for (var r = rows - 1; r >= 0; r--) {
        var line = "";
        for (var c = 0; c < cols; c++) {
          var lvl = Math.round(levels[c] - r * 8);
          line += glyphs[lvl >= 8 ? 8 : lvl <= 0 ? 0 : lvl];
        }
        out.push(line);
      }
      pre.textContent = out.join("\n");
    }

    function tick() { frame(performance.now()); }
    function sync() {
      var run = visible && !document.hidden && !still.matches;
      if (run && !timer) timer = setInterval(tick, 1000 / fps);
      if (!run && timer) { clearInterval(timer); timer = null; }
    }

    measure();
    // A still frame for reduced motion, and the first one otherwise.
    for (var warm = 0; warm < 20; warm++) frame(4000 + warm * 40);
    window.addEventListener("resize", function () { measure(); tick(); });
    document.addEventListener("visibilitychange", sync);
    if (still.addEventListener) still.addEventListener("change", sync);
    if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (e) { visible = e[0].isIntersecting; sync(); }).observe(pre);
    }
    sync();
  }

  // Dial: light up the station under the needle as the scale drifts by.
  function dial(el) {
    var needle = el.querySelector(".needle");
    var names = el.querySelectorAll("li");
    var lit = null;
    function check() {
      var n = needle.getBoundingClientRect();
      var x = n.left + n.width / 2, hit = null;
      for (var i = 0; i < names.length; i++) {
        var b = names[i].getBoundingClientRect();
        if (b.left <= x && b.right >= x) { hit = names[i]; break; }
      }
      if (hit !== lit) {
        if (lit) lit.classList.remove("tuned");
        if (hit) hit.classList.add("tuned");
        lit = hit;
      }
    }
    check();
    setInterval(function () { if (!document.hidden && !still.matches) check(); }, 120);
  }

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

  // Copy the quick start's commands without their prompts.
  function copyButtons() {
    if (!navigator.clipboard) return;
    document.querySelectorAll(".copy").forEach(function (btn) {
      var code = btn.parentNode.querySelector("pre code");
      btn.hidden = false;
      btn.addEventListener("click", function () {
        var text = code.textContent.split("\n").map(function (l) { return l.replace(/^\$\s*/, ""); }).join("\n");
        navigator.clipboard.writeText(text).then(function () {
          btn.textContent = "copied";
          setTimeout(function () { btn.textContent = "copy"; }, 1600);
        });
      });
    });
  }

  var pre = document.querySelector(".spectrum");
  if (pre) spectrum(pre);
  var d = document.querySelector(".dial");
  if (d) dial(d);
  shortcuts();
  copyButtons();
})();
