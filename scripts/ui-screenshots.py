#!/usr/bin/env python3
"""Before/after screenshots of HYB2 Web Lite with the included ZIKV example loaded.

Usage (from the repository root):
    pip install playwright && python -m playwright install chromium
    python scripts/ui-screenshots.py --out /tmp/shots/before
    python scripts/ui-screenshots.py --out /tmp/shots/after --pages landing,overview,compare --modes desk,mob

Serves ./web on a local port, clicks "Try example", waits until the example's Compare
files are loaded, runs the differential test and the folding energies on the Compare
page (so panels A, D-G and the report's figure panels are filled; --no-tests skips
this), then captures each page as <mode>_<page>.png (viewport) and
<mode>_<page>_full.png (full page) once its data has arrived.
Also prints any page whose document is wider than the viewport (horizontal overflow).
Folding energies need the ViennaRNA build in web/vendor/viennarna (not in git); without
it they are skipped with a warning.
"""
import argparse
import functools
import http.server
import os
import socketserver
import threading

from playwright.sync_api import sync_playwright

PAGES = ["landing", "prepared", "overview", "interactions", "contact-map", "viewpoint",
         "region", "compare", "structure", "report"]
MODES = {
    "desk": {"viewport": {"width": 1440, "height": 900}},
    "mob": {"viewport": {"width": 390, "height": 844}, "is_mobile": True, "has_touch": True, "device_scale_factor": 2},
    "dark": {"viewport": {"width": 1440, "height": 900}, "color_scheme": "dark"},
}


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def serve(root):
    handler = functools.partial(QuietHandler, directory=root)
    httpd = socketserver.TCPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd, "http://127.0.0.1:%d/" % httpd.server_address[1]


def wait_ready(page, timeout=90000):
    # the page has drawn its data: no refresh in progress, no comparison progress bar,
    # and (Report page) no step still being prepared
    page.wait_for_function("""() => {
        if (document.querySelector('.is-refreshing-data')) return false;
        if ([...document.querySelectorAll('[data-comparison-progress]')].some(n => !n.hidden)) return false;
        if (document.querySelector('[data-status="pending"], [data-status="running"]')) return false;
        return true;
    }""", timeout=timeout)
    page.wait_for_timeout(600)


def run_tests(page):
    # differential test (panel A) and folding energies (F, G); the arc map and the
    # region structures (C-E) fill from the test's result
    page.evaluate("location.hash = '#/workspace/compare'")
    page.locator('[data-feature-action="deseq-run"]').wait_for(timeout=60000)
    page.locator('[data-feature-action="deseq-run"]').click()
    page.locator('[data-feature-action="deseq-export-interactions"]').wait_for(timeout=300000)
    print("  differential test done")
    energy = page.locator('[data-feature-action="energy-run"]')
    if energy.count() and energy.first.is_enabled():
        energy.first.click()
        try:
            page.locator('[data-feature-action="energy-download-svg"]').wait_for(timeout=180000)
            print("  folding energies done")
        except Exception:
            print("  ! folding energies did not finish (is web/vendor/viennarna built?)")
    else:
        print("  ! folding energies not available")


def reveal(page):
    # landing cards animate in on scroll; walk the page once so full-page shots show them
    height = page.evaluate("document.documentElement.scrollHeight")
    for _ in range(0, height, 300):
        page.mouse.wheel(0, 300)
        page.wait_for_timeout(80)
    page.wait_for_timeout(800)
    page.evaluate("window.scrollTo({ top: 0, left: 0, behavior: 'instant' })")
    page.wait_for_timeout(300)


def shoot(page, out, mode, name):
    page.screenshot(path=os.path.join(out, "%s_%s.png" % (mode, name)))
    page.screenshot(path=os.path.join(out, "%s_%s_full.png" % (mode, name)), full_page=True)
    overflow = page.evaluate("document.documentElement.scrollWidth - window.innerWidth")
    if overflow > 0:
        print("  ! %s/%s: horizontal overflow %dpx" % (mode, name, overflow))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", required=True)
    parser.add_argument("--pages", default=",".join(PAGES))
    parser.add_argument("--modes", default="desk,mob,dark")
    parser.add_argument("--web", default=os.path.join(os.path.dirname(__file__), "..", "web"))
    parser.add_argument("--no-tests", action="store_true", help="do not run the differential test and folding energies")
    args = parser.parse_args()
    wanted = args.pages.split(",")
    os.makedirs(args.out, exist_ok=True)
    httpd, base = serve(os.path.abspath(args.web))
    errors = []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        for mode in args.modes.split(","):
            context = browser.new_context(**MODES[mode])
            page = context.new_page()
            page.on("pageerror", lambda e: errors.append(str(e)))
            page.goto(base, wait_until="networkidle")
            page.wait_for_timeout(800)
            if "landing" in wanted:
                reveal(page)
                shoot(page, args.out, mode, "landing")
            if not set(wanted) - {"landing"}:
                context.close()
                continue
            page.get_by_role("button", name="Try example").click()
            page.get_by_role("button", name="Enter analysis").wait_for(timeout=120000)
            # the replicates and arc map files load after the primary file
            page.get_by_text("Compare page ready", exact=False).wait_for(timeout=180000)
            if "prepared" in wanted:
                page.wait_for_timeout(600)
                shoot(page, args.out, mode, "prepared")
            page.get_by_role("button", name="Enter analysis").click()
            page.wait_for_timeout(1500)
            if not args.no_tests and set(wanted) & {"compare", "report"}:
                run_tests(page)
            for name in wanted:
                if name in ("landing", "prepared"):
                    continue
                page.evaluate("location.hash = '#/workspace/%s'" % name)
                page.wait_for_timeout(500)
                try:
                    wait_ready(page)
                except Exception:
                    print("  ! %s/%s: still loading after 90 s; shot taken anyway" % (mode, name))
                page.evaluate("window.scrollTo({ top: 0, left: 0, behavior: 'instant' })")
                shoot(page, args.out, mode, name)
                print("%s/%s ok" % (mode, name))
            context.close()
        browser.close()
    httpd.shutdown()
    if errors:
        print("page errors:\n  " + "\n  ".join(errors[:20]))


if __name__ == "__main__":
    main()
