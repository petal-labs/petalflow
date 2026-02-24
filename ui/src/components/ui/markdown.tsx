import { Fragment, useMemo } from 'react'
import { cn } from '@/lib/utils'

interface MarkdownProps {
  content: string
  className?: string
}

type MarkdownBlock =
  | { type: 'heading'; level: 1 | 2 | 3 | 4 | 5 | 6; text: string }
  | { type: 'paragraph'; text: string }
  | { type: 'code'; language?: string; code: string }
  | { type: 'unordered-list'; items: string[] }
  | { type: 'ordered-list'; items: string[] }
  | { type: 'blockquote'; text: string }

function sanitizeLink(url: string): string | undefined {
  const trimmed = url.trim()
  if (/^(https?:\/\/|mailto:)/i.test(trimmed)) {
    return trimmed
  }
  return undefined
}

function renderInline(text: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = []
  const pattern = /\[([^\]]+)\]\(([^)]+)\)|`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_/g
  let cursor = 0
  let match = pattern.exec(text)

  while (match) {
    if (match.index > cursor) {
      nodes.push(text.slice(cursor, match.index))
    }

    if (match[1] && match[2]) {
      const href = sanitizeLink(match[2])
      if (href) {
        nodes.push(
          <a
            key={`${match.index}-link`}
            href={href}
            target="_blank"
            rel="noopener noreferrer"
            className="text-blue underline underline-offset-2"
          >
            {match[1]}
          </a>
        )
      } else {
        nodes.push(match[1])
      }
    } else if (match[3]) {
      nodes.push(
        <code key={`${match.index}-code`} className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[12px]">
          {match[3]}
        </code>
      )
    } else if (match[4] || match[5]) {
      nodes.push(<strong key={`${match.index}-strong`}>{match[4] || match[5]}</strong>)
    } else if (match[6] || match[7]) {
      nodes.push(<em key={`${match.index}-em`}>{match[6] || match[7]}</em>)
    }

    cursor = pattern.lastIndex
    match = pattern.exec(text)
  }

  if (cursor < text.length) {
    nodes.push(text.slice(cursor))
  }
  return nodes
}

function isUnorderedListLine(line: string): boolean {
  return /^\s*[-*]\s+/.test(line)
}

function isOrderedListLine(line: string): boolean {
  return /^\s*\d+\.\s+/.test(line)
}

function parseMarkdownBlocks(content: string): MarkdownBlock[] {
  const lines = content.replace(/\r/g, '').split('\n')
  const blocks: MarkdownBlock[] = []
  let idx = 0

  while (idx < lines.length) {
    const line = lines[idx]
    const trimmed = line.trim()
    if (!trimmed) {
      idx += 1
      continue
    }

    const headingMatch = /^(#{1,6})\s+(.*)$/.exec(trimmed)
    if (headingMatch) {
      const level = headingMatch[1].length as 1 | 2 | 3 | 4 | 5 | 6
      blocks.push({ type: 'heading', level, text: headingMatch[2].trim() })
      idx += 1
      continue
    }

    if (trimmed.startsWith('```')) {
      const language = trimmed.slice(3).trim() || undefined
      const codeLines: string[] = []
      idx += 1
      while (idx < lines.length && !lines[idx].trim().startsWith('```')) {
        codeLines.push(lines[idx])
        idx += 1
      }
      if (idx < lines.length && lines[idx].trim().startsWith('```')) {
        idx += 1
      }
      blocks.push({ type: 'code', language, code: codeLines.join('\n') })
      continue
    }

    if (isUnorderedListLine(line) || isOrderedListLine(line)) {
      const ordered = isOrderedListLine(line)
      const items: string[] = []
      while (
        idx < lines.length &&
        (ordered ? isOrderedListLine(lines[idx]) : isUnorderedListLine(lines[idx]))
      ) {
        items.push(lines[idx].replace(ordered ? /^\s*\d+\.\s+/ : /^\s*[-*]\s+/, '').trim())
        idx += 1
      }
      blocks.push({ type: ordered ? 'ordered-list' : 'unordered-list', items })
      continue
    }

    if (trimmed.startsWith('>')) {
      const quoteLines: string[] = []
      while (idx < lines.length && lines[idx].trim().startsWith('>')) {
        quoteLines.push(lines[idx].replace(/^\s*>\s?/, '').trim())
        idx += 1
      }
      blocks.push({ type: 'blockquote', text: quoteLines.join(' ') })
      continue
    }

    const paragraphLines: string[] = []
    while (
      idx < lines.length &&
      lines[idx].trim() &&
      !/^(#{1,6})\s+/.test(lines[idx].trim()) &&
      !lines[idx].trim().startsWith('```') &&
      !isUnorderedListLine(lines[idx]) &&
      !isOrderedListLine(lines[idx]) &&
      !lines[idx].trim().startsWith('>')
    ) {
      paragraphLines.push(lines[idx].trim())
      idx += 1
    }
    blocks.push({ type: 'paragraph', text: paragraphLines.join(' ') })
  }

  return blocks
}

function headingClass(level: number): string {
  if (level === 1) return 'text-[20px] font-bold mt-1 mb-2'
  if (level === 2) return 'text-[17px] font-bold mt-1 mb-2'
  if (level === 3) return 'text-[15px] font-semibold mt-1 mb-1.5'
  return 'text-sm font-semibold mt-1 mb-1.5'
}

export function Markdown({ content, className }: MarkdownProps) {
  const blocks = useMemo(() => parseMarkdownBlocks(content), [content])

  return (
    <div className={cn('text-sm leading-6 text-foreground break-words', className)}>
      {blocks.map((block, index) => {
        if (block.type === 'heading') {
          const Tag = `h${block.level}` as keyof JSX.IntrinsicElements
          return (
            <Tag key={`heading-${index}`} className={headingClass(block.level)}>
              {renderInline(block.text)}
            </Tag>
          )
        }

        if (block.type === 'code') {
          return (
            <div key={`code-${index}`} className="my-2 overflow-auto rounded-lg border border-border bg-surface-0">
              {block.language && (
                <div className="border-b border-border px-3 py-1 text-[10px] uppercase tracking-wide text-muted-foreground">
                  {block.language}
                </div>
              )}
              <pre className="p-3 font-mono text-[12px] leading-5 text-foreground">
                <code>{block.code}</code>
              </pre>
            </div>
          )
        }

        if (block.type === 'unordered-list' || block.type === 'ordered-list') {
          const ListTag = block.type === 'ordered-list' ? 'ol' : 'ul'
          return (
            <ListTag
              key={`list-${index}`}
              className={cn('my-1 pl-5', block.type === 'ordered-list' ? 'list-decimal' : 'list-disc')}
            >
              {block.items.map((item, itemIdx) => (
                <li key={`item-${index}-${itemIdx}`} className="my-0.5">
                  {renderInline(item)}
                </li>
              ))}
            </ListTag>
          )
        }

        if (block.type === 'blockquote') {
          return (
            <blockquote
              key={`quote-${index}`}
              className="my-2 border-l-2 border-border pl-3 text-muted-foreground"
            >
              {renderInline(block.text)}
            </blockquote>
          )
        }

        return (
          <p key={`paragraph-${index}`} className="my-1">
            {renderInline(block.text)}
          </p>
        )
      })}
      {blocks.length === 0 && <Fragment />}
    </div>
  )
}
