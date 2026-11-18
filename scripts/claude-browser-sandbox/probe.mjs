// What a browser-checking Claude worker does in its sandbox: serve a page on
// 127.0.0.1, open it in headless Playwright, and save screenshots at two widths.
// Usage: node probe.mjs <chromium|webkit> <output folder>
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { chromium, webkit } from "playwright-core";

const [engine = "chromium", outDir = "."] = process.argv.slice(2);
const launcher = { chromium, webkit }[engine];
if (!launcher) {
  console.error(`unknown engine ${engine}`);
  process.exit(2);
}
fs.mkdirSync(outDir, { recursive: true });

const server = http.createServer((_, res) => {
  res.setHeader("content-type", "text/html");
  res.end("<h1 style='font-family:sans-serif'>sandbox probe</h1>");
});
await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(0, "127.0.0.1", resolve);
});
const url = `http://127.0.0.1:${server.address().port}/`;
console.log(`serve ok ${url}`);

const browser = await launcher.launch({ headless: true });
try {
  for (const width of [390, 1280]) {
    const page = await browser.newPage({ viewport: { width, height: 800 } });
    await page.goto(url);
    const file = path.join(outDir, `${engine}-${width}.png`);
    await page.screenshot({ path: file });
    console.log(`screenshot ok ${file} ${fs.statSync(file).size} bytes`);
    await page.close();
  }
} finally {
  await browser.close();
  server.close();
}
