/**
 * A small Markdown reader for changelogs and release notes: headings, bullet
 * lists, paragraphs, `code`, **bold** and links. It produces plain data that
 * the template renders, so no HTML from the text is ever inserted.
 */

export type InlinePart =
  | { kind: 'text', text: string }
  | { kind: 'code', text: string }
  | { kind: 'bold', text: string }
  | { kind: 'link', text: string, url: string }

export type MarkdownBlock =
  | { kind: 'heading', level: 1 | 2 | 3, parts: InlinePart[] }
  | { kind: 'list', items: InlinePart[][] }
  | { kind: 'paragraph', parts: InlinePart[] }

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\((https?:\/\/[^)\s]+)\))|(https?:\/\/[^\s)]+)/g

export function parseInline(text: string): InlinePart[] {
  const parts: InlinePart[] = []
  let last = 0
  for (const match of text.matchAll(INLINE)) {
    const index = match.index ?? 0
    if (index > last) parts.push({ kind: 'text', text: text.slice(last, index) })
    const [whole, code, bold, link, linkUrl, bareUrl] = match
    if (code) parts.push({ kind: 'code', text: code.slice(1, -1) })
    else if (bold) parts.push({ kind: 'bold', text: bold.slice(2, -2) })
    else if (link && linkUrl) parts.push({ kind: 'link', text: link.slice(1, link.indexOf('](')), url: linkUrl })
    else if (bareUrl) parts.push({ kind: 'link', text: bareUrl, url: bareUrl })
    last = index + whole.length
  }
  if (last < text.length) parts.push({ kind: 'text', text: text.slice(last) })
  return parts
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  const blocks: MarkdownBlock[] = []
  let paragraph: string[] = []
  let list: string[] | null = null

  const flushParagraph = () => {
    if (paragraph.length) blocks.push({ kind: 'paragraph', parts: parseInline(paragraph.join(' ')) })
    paragraph = []
  }
  const flushList = () => {
    if (list) blocks.push({ kind: 'list', items: list.map(parseInline) })
    list = null
  }

  for (const raw of source.replace(/\r/g, '').split('\n')) {
    const line = raw.trim()
    const heading = /^(#{1,3})\s+(.*)$/.exec(line)
    const bullet = /^[-*]\s+(.*)$/.exec(line)
    if (!line || /^<!--.*-->$/.test(line)) {
      flushParagraph()
      flushList()
    } else if (heading) {
      flushParagraph()
      flushList()
      blocks.push({ kind: 'heading', level: heading[1]!.length as 1 | 2 | 3, parts: parseInline(heading[2]!) })
    } else if (bullet) {
      flushParagraph()
      list = list ?? []
      list.push(bullet[1]!)
    } else if (list && /^\s/.test(raw)) {
      // A wrapped continuation of the previous bullet.
      list[list.length - 1] += ' ' + line
    } else {
      flushList()
      paragraph.push(line)
    }
  }
  flushParagraph()
  flushList()
  return blocks
}
