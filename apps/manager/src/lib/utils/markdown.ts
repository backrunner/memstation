import DOMPurify from 'isomorphic-dompurify';
import { marked } from 'marked';

marked.setOptions({
  breaks: true,
  gfm: true
});

export function renderMarkdown(value: string): string {
  if (!value.trim()) {
    return '';
  }
  const rendered = marked.parse(value) as string;
  return DOMPurify.sanitize(rendered);
}
