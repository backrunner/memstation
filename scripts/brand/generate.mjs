import { readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';

const root = new URL('../../', import.meta.url);
const require = createRequire(new URL('apps/manager/package.json', root));
const { chromium } = require('@playwright/test');
const source = new URL('docs/assets/memstation-source.png', root);
const dataURL = `data:image/png;base64,${(await readFile(source)).toString('base64')}`;

const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  const exports = await page.evaluate(async (url) => {
    const sourceImage = new Image();
    sourceImage.src = url;
    await sourceImage.decode();
    const sourceCanvas = document.createElement('canvas');
    sourceCanvas.width = sourceImage.naturalWidth;
    sourceCanvas.height = sourceImage.naturalHeight;
    const sourceContext = sourceCanvas.getContext('2d');
    sourceContext.drawImage(sourceImage, 0, 0);
    const { data } = sourceContext.getImageData(0, 0, sourceCanvas.width, sourceCanvas.height);
    let left = sourceCanvas.width, top = sourceCanvas.height, right = -1, bottom = -1;
    for (let y = 0; y < sourceCanvas.height; y++) {
      for (let x = 0; x < sourceCanvas.width; x++) {
        if (data[(y * sourceCanvas.width + x) * 4 + 3] < 128) continue;
        left = Math.min(left, x);
        right = Math.max(right, x);
        top = Math.min(top, y);
        bottom = Math.max(bottom, y);
      }
    }
    if (right < left) throw new Error('Logo source has no visible pixels.');
    // Include the antialiased edge; preserve the generated image's original alpha.
    left = Math.max(0, left - 2);
    top = Math.max(0, top - 2);
    right = Math.min(sourceCanvas.width - 1, right + 2);
    bottom = Math.min(sourceCanvas.height - 1, bottom + 2);
    const width = right - left + 1, height = bottom - top + 1;
    return [
      [16, 'favicon-16.png', 'favicon'],
      [32, 'favicon.png', 'favicon'],
      [96, 'brand/memstation-96.png', 'light'],
      [180, 'apple-touch-icon.png', 'touch'],
      [512, 'brand/memstation.png', 'light'],
      [512, 'brand/memstation-dark.png', 'dark']
    ].map(([size, target, variant]) => {
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = size;
      const context = canvas.getContext('2d');
      if (variant === 'touch' || variant === 'favicon') {
        context.fillStyle = '#f4f7f4';
        context.beginPath();
        context.roundRect(0, 0, size, size, variant === 'favicon' ? size * 0.15 : 0);
        context.fill();
      }
      const scale = size * (variant === 'touch' ? 0.8 : 0.92) / Math.max(width, height);
      context.imageSmoothingQuality = 'high';
      context.drawImage(sourceImage, left, top, width, height,
        (size - width * scale) / 2, (size - height * scale) / 2, width * scale, height * scale);
      if (variant === 'dark') {
        context.globalCompositeOperation = 'source-in';
        context.fillStyle = '#d8ebdf';
        context.fillRect(0, 0, size, size);
      }
      return { target, png: canvas.toDataURL('image/png').split(',')[1] };
    });
  }, dataURL);
  for (const { target, png } of exports) {
    await writeFile(new URL(`apps/manager/static/${target}`, root), Buffer.from(png, 'base64'));
  }
} finally {
  await browser.close();
}
console.log('Generated 16/32px favicons, 96/512px logos, a dark-background variant and an opaque 180px Apple touch icon.');
