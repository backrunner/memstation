type ShikiModule = typeof import('shiki');
type BundledLanguage = import('shiki').BundledLanguage;
type ShikiHighlighter = Awaited<ReturnType<ShikiModule['getSingletonHighlighter']>>;
type HighlightLanguage = BundledLanguage | 'text' | 'plaintext' | 'txt' | 'plain' | 'ansi';

const SHIKI_THEME = 'github-light-high-contrast';
const SPECIAL_TEXT_LANGUAGES = new Set<string>(['text', 'plaintext', 'txt', 'plain', 'ansi']);
const LANGUAGE_ALIASES: Record<string, string> = {
  cjs: 'javascript',
  console: 'bash',
  env: 'bash',
  htm: 'html',
  js: 'javascript',
  json5: 'json',
  jsx: 'jsx',
  md: 'markdown',
  mjs: 'javascript',
  sh: 'bash',
  shell: 'bash',
  text: 'text',
  plaintext: 'text',
  txt: 'text',
  ts: 'typescript',
  yml: 'yaml',
  zsh: 'bash'
};

let shikiModulePromise: Promise<ShikiModule> | null = null;
let highlighterPromise: Promise<ShikiHighlighter> | null = null;
const failedLanguages = new Set<string>();

function loadShikiModule(): Promise<ShikiModule> {
  if (!shikiModulePromise) {
    shikiModulePromise = import('shiki');
  }
  return shikiModulePromise;
}

export function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export async function getShikiHighlighter(): Promise<ShikiHighlighter> {
  if (!highlighterPromise) {
    highlighterPromise = loadShikiModule().then((shiki) =>
      shiki.getSingletonHighlighter({
        engine: shiki.createJavaScriptRegexEngine(),
        langs: ['text', 'json', 'bash', 'markdown', 'javascript', 'typescript'],
        themes: [SHIKI_THEME]
      })
    );
  }
  return highlighterPromise;
}

async function resolveLanguage(language?: string): Promise<HighlightLanguage> {
  const candidate = (language ?? 'text')
    .trim()
    .toLowerCase()
    .replace(/^language-/, '');
  const alias = (LANGUAGE_ALIASES[candidate] ?? candidate) || 'text';

  if (SPECIAL_TEXT_LANGUAGES.has(alias)) {
    return alias === 'ansi' ? 'ansi' : 'text';
  }
  if (failedLanguages.has(alias)) {
    return 'text';
  }

  const shiki = await loadShikiModule();
  if (alias in shiki.bundledLanguages || alias in shiki.bundledLanguagesAlias) {
    return alias as HighlightLanguage;
  }
  return 'text';
}

async function ensureLanguage(highlighter: ShikiHighlighter, language: HighlightLanguage): Promise<HighlightLanguage> {
  if (SPECIAL_TEXT_LANGUAGES.has(language)) {
    return language === 'ansi' ? 'ansi' : 'text';
  }
  if (highlighter.getLoadedLanguages().includes(language)) {
    return language;
  }
  try {
    await highlighter.loadLanguage(language);
    return language;
  } catch {
    failedLanguages.add(language);
    return 'text';
  }
}

function extractLanguageFromElement(codeElement: HTMLElement, preElement: HTMLElement): string {
  const fromDataset = codeElement.dataset.language ?? preElement.dataset.language;
  if (fromDataset) {
    return fromDataset;
  }
  const languageClass = [...codeElement.classList].find((className) => className.startsWith('language-'));
  if (languageClass) {
    return languageClass.slice('language-'.length);
  }
  return 'text';
}

export async function highlightCodeToHtml(code: string, language?: string): Promise<string> {
  const highlighter = await getShikiHighlighter();
  const resolved = await resolveLanguage(language);
  const ensured = await ensureLanguage(highlighter, resolved);

  return highlighter.codeToHtml(code, {
    lang: ensured,
    theme: SHIKI_THEME,
    transformers: [
      {
        pre(node) {
          this.addClassToHast(node, ['code-block-surface']);
          node.properties['data-language'] = ensured;
        }
      }
    ]
  });
}

export async function enhanceCodeBlocks(container: HTMLElement): Promise<void> {
  const codeBlocks = [...container.querySelectorAll<HTMLElement>('pre > code')];

  for (const codeElement of codeBlocks) {
    const preElement = codeElement.parentElement;
    if (!(preElement instanceof HTMLElement) || preElement.dataset.shikiEnhanced) {
      continue;
    }

    const code = codeElement.textContent ?? '';
    const language = extractLanguageFromElement(codeElement, preElement);
    preElement.dataset.shikiEnhanced = 'pending';

    try {
      const html = await highlightCodeToHtml(code, language);
      if (!preElement.isConnected || !container.contains(preElement)) {
        continue;
      }

      const wrapper = document.createElement('div');
      wrapper.innerHTML = html;
      const highlighted = wrapper.firstElementChild;
      if (!(highlighted instanceof HTMLElement)) {
        preElement.dataset.shikiEnhanced = 'true';
        continue;
      }

      highlighted.dataset.shikiEnhanced = 'true';
      preElement.replaceWith(highlighted);
    } catch {
      preElement.dataset.shikiEnhanced = 'true';
      preElement.classList.add('code-block-surface', 'code-block-fallback');
      preElement.dataset.language = language;
    }
  }
}
