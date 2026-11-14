import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const directory = path.dirname(fileURLToPath(import.meta.url));
const read = (name) => readFile(path.join(directory, name), 'utf8');
const [template, styles, fixtures, app] = await Promise.all([
  read('index.template.html'), read('styles.css'), read('fixtures.js'), read('app.js'),
]);
const script = (value) => value.replaceAll('</script', '<\\/script');
const output = template
  .replace('/* INLINE_STYLES */', () => styles)
  .replace('/* INLINE_FIXTURES */', () => script(fixtures))
  .replace('/* INLINE_APP */', () => script(app));
await writeFile(path.join(directory, 'index.html'), output);
if (process.argv[2]) {
  const destination = path.resolve(process.argv[2]);
  await mkdir(path.dirname(destination), { recursive: true });
  await writeFile(destination, output);
}
console.log(JSON.stringify({ artifact: path.join(directory, 'index.html'), bytes: Buffer.byteLength(output), selfContained: true }));
