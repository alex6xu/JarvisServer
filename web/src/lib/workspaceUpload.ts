import JSZip from 'jszip'

export const MAX_UPLOAD_FILE_BYTES = 3 * 1024 * 1024 // 3MB

export type FilteredFile = File & { webkitRelativePath?: string }

export interface BuildWorkspaceZipResult {
  blob: Blob
  name: string
  included: number
  skippedHidden: number
  skippedLarge: number
  skippedOther: number
}

/** True if any path segment is a hidden file/dir (name starts with '.'). */
export function isHiddenRelativePath(rel: string): boolean {
  const parts = rel.replace(/\\/g, '/').split('/').filter(Boolean)
  return parts.some((p) => p.startsWith('.'))
}

export function shouldSkipRelativePath(rel: string): 'hidden' | 'vendor' | null {
  const norm = rel.replace(/\\/g, '/')
  if (isHiddenRelativePath(norm)) return 'hidden'
  if (
    norm.includes('/node_modules/') ||
    norm.startsWith('node_modules/') ||
    norm.includes('/__MACOSX/') ||
    norm.startsWith('__MACOSX/')
  ) {
    return 'vendor'
  }
  return null
}

/** Detect shared first path segment (webkitdirectory folder name). */
export function detectCommonRoot(paths: string[]): string {
  let root = ''
  for (const raw of paths) {
    const p = raw.replace(/\\/g, '/').replace(/^\.\//, '')
    if (!p) continue
    const parts = p.split('/').filter(Boolean)
    if (parts.length < 2) {
      // A file without a folder wrapper — do not strip.
      return ''
    }
    if (!root) {
      root = parts[0]
      continue
    }
    if (parts[0] !== root) return ''
  }
  return root
}

export function stripRootPrefix(rel: string, root: string): string {
  if (!root) return rel.replace(/\\/g, '/')
  const norm = rel.replace(/\\/g, '/')
  if (norm === root) return ''
  if (norm.startsWith(root + '/')) return norm.slice(root.length + 1)
  return norm
}

/** Collect parent directory paths for explicit zip folder entries. */
export function collectDirPrefixes(relPaths: string[]): string[] {
  const dirs = new Set<string>()
  for (const rel of relPaths) {
    const parts = rel.replace(/\\/g, '/').split('/').filter(Boolean)
    let cur = ''
    for (let i = 0; i < parts.length - 1; i++) {
      cur = cur ? `${cur}/${parts[i]}` : parts[i]
      dirs.add(cur)
    }
  }
  return Array.from(dirs).sort()
}

/**
 * Filters a webkitdirectory FileList, strips the shared project folder so the
 * workspace root matches the project root, materializes directory entries,
 * then builds a zip archive for upload as FormData field "archive".
 */
export async function buildWorkspaceZipFromDirectory(
  files: FileList | FilteredFile[]
): Promise<BuildWorkspaceZipResult> {
  const list = Array.from(files as ArrayLike<FilteredFile>)
  if (list.length === 0) {
    throw new Error('目录为空')
  }

  const rawPaths = list.map((f) => (f.webkitRelativePath || f.name || '').replace(/\\/g, '/'))
  const root = detectCommonRoot(rawPaths)
  const name = root || rawPaths[0]?.split('/')[0] || 'project'

  const zip = new JSZip()
  let included = 0
  let skippedHidden = 0
  let skippedLarge = 0
  let skippedOther = 0
  const keptRels: string[] = []

  for (const file of list) {
    const raw = (file.webkitRelativePath || file.name || '').replace(/\\/g, '/')
    if (!raw) {
      skippedOther++
      continue
    }
    const skip = shouldSkipRelativePath(raw)
    if (skip === 'hidden') {
      skippedHidden++
      continue
    }
    if (skip === 'vendor') {
      skippedOther++
      continue
    }
    if (file.size > MAX_UPLOAD_FILE_BYTES) {
      skippedLarge++
      continue
    }
    const rel = stripRootPrefix(raw, root)
    if (!rel || rel.endsWith('/')) {
      skippedOther++
      continue
    }
    zip.file(rel, file)
    keptRels.push(rel)
    included++
    if (included >= 5000) break
  }

  if (included === 0) {
    throw new Error('没有可上传的文件（已过滤隐藏文件、node_modules 及超过 3MB 的文件）')
  }

  // Explicit directory entries keep empty intermediate folders after extract.
  for (const dir of collectDirPrefixes(keptRels)) {
    zip.folder(dir)
  }

  const blob = await zip.generateAsync({
    type: 'blob',
    compression: 'DEFLATE',
    compressionOptions: { level: 6 },
  })

  return { blob, name, included, skippedHidden, skippedLarge, skippedOther }
}

export function formatUploadSkipSummary(r: BuildWorkspaceZipResult): string {
  const parts: string[] = []
  if (r.skippedHidden > 0) parts.push(`隐藏 ${r.skippedHidden}`)
  if (r.skippedLarge > 0) parts.push(`>3MB ${r.skippedLarge}`)
  if (r.skippedOther > 0) parts.push(`其它 ${r.skippedOther}`)
  if (parts.length === 0) return ''
  return `，已跳过：${parts.join('、')}`
}
