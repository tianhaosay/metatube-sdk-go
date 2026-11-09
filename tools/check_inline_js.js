// Extract the inline <script> from an HTML file so node --check can parse it.
// Usage: node tools/check_inline_js.js <html-file>
const fs = require('fs');

const file = process.argv[2];
if (!file) {
  console.error('usage: check_inline_js.js <html-file>');
  process.exit(2);
}
const html = fs.readFileSync(file, 'utf8');
const match = /<script>([\s\S]*?)<\/script>/.exec(html);
if (!match) {
  console.error('no inline <script> found in ' + file);
  process.exit(1);
}
process.stdout.write(match[1]);
