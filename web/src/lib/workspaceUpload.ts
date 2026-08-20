import JSZip from 'jszip'
import ignore from 'ignore'

export const MAX_UPLOAD_FILE_BYTES = 10 * 1024 * 1024 // 10MB
export const MAX_UPLOAD_TOTAL_BYTES = 100 * 1024 * 1024 // 100MB
export const MAX_UPLOAD_FILES = 5000

export interface WorkspaceUploadLimits {
  archiveBytes: number
  uncompressedBytes: number
  fileBytes: number
  maxFiles: number
}

export const DEFAULT_WORKSPACE_UPLOAD_LIMITS: WorkspaceUploadLimits = {
  archiveBytes: MAX_UPLOAD_TOTAL_BYTES,
  uncompressedBytes: MAX_UPLOAD_TOTAL_BYTES,
  fileBytes: MAX_UPLOAD_FILE_BYTES,
  maxFiles: MAX_UPLOAD_FILES,
}

export type FilteredFile = File & { webkitRelativePath?: string }

export interface BuildWorkspaceZipResult {
  blob: Blob
  name: string
  included: number
  skippedHidden: number
  skippedLarge: number
  skippedOther: number
}

/** True for VCS internals and common credential files that must not leave the browser. */
export function isHiddenRelativePath(rel: string): boolean {
  const parts = rel.replace(/\\/g, '/').split('/').filter(Boolean)
  if (parts.some((part) => ['.git', '.hg', '.svn'].includes(part.toLowerCase()))) return true
  const filename = parts.length > 0 ? parts[parts.length - 1].toLowerCase() : ''
  if (['.npmrc', '.yarnrc', '.pypirc', '.netrc'].includes(filename)) return true
  if (!filename.startsWith('.env')) return false
  return !['.env.example', '.env.sample', '.env.template'].includes(filename)
}

export function shouldSkipRelativePath(rel: string): 'hidden' | 'vendor' | null {
  const norm = rel.replace(/\\/g, '/')
  if (isHiddenRelativePath(norm)) return 'hidden'
  const parts = norm.split('/').filter(Boolean).map((part) => part.toLowerCase())
  if (parts.includes('__macosx')) return 'vendor'
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

interface GitignoreRules {
  base: string
  filePath: string
  matcher: ReturnType<typeof ignore>
}

/** Build Git-compatible matchers for root and nested .gitignore files. */
export async function buildProjectIgnoreMatcher(
  files: FilteredFile[],
  root: string,
): Promise<(relativePath: string) => boolean> {
  const ruleFiles = files.flatMap((file) => {
    const raw = (file.webkitRelativePath || file.name || '').replace(/\\/g, '/')
    if (!raw) return []
    const relativePath = stripRootPrefix(raw, root)
    if (relativePath.split('/').pop() !== '.gitignore') return []
    const slash = relativePath.lastIndexOf('/')
    return [{ file, filePath: relativePath, base: slash < 0 ? '' : relativePath.slice(0, slash) }]
  })
  const depth = (base: string) => base ? base.split('/').length : 0
  ruleFiles.sort((a, b) => depth(a.base) - depth(b.base))

  const rules: GitignoreRules[] = []
  for (const ruleFile of ruleFiles) {
    try {
      rules.push({
        base: ruleFile.base,
        filePath: ruleFile.filePath,
        matcher: ignore().add(await ruleFile.file.text()),
      })
    } catch {
      // An unreadable .gitignore must not make the whole directory impossible to upload.
    }
  }
  return (relativePath: string) => {
    if (rules.some((rule) => rule.filePath === relativePath)) return false
    let ignored = false
    for (const rule of rules) {
      if (rule.base && !relativePath.startsWith(`${rule.base}/`)) continue
      const scopedPath = rule.base ? relativePath.slice(rule.base.length + 1) : relativePath
      const result = rule.matcher.test(scopedPath)
      if (result.ignored) ignored = true
      if (result.unignored) ignored = false
    }
    return ignored
  }
}

export function normalizeArchiveRelativePath(rel: string): string {
  const norm = rel.replace(/\\/g, '/')
  if (!norm || norm.startsWith('/') || norm.includes('\0')) {
    throw new Error(`无效的文件路径：${rel || '(空路径)'}`)
  }
  const parts = norm.split('/')
  if (parts.some((part) => !part || part === '.' || part === '..' || part.includes(':'))) {
    throw new Error(`无效的文件路径：${rel}`)
  }
  return parts.join('/')
}

export function assertUploadCapacity(
  included: number,
  totalBytes: number,
  nextFileBytes: number,
  limits: WorkspaceUploadLimits = DEFAULT_WORKSPACE_UPLOAD_LIMITS,
): void {
  if (included >= limits.maxFiles) {
    throw new Error(`可上传文件超过 ${limits.maxFiles} 个，请减少文件后重试`)
  }
  if (nextFileBytes > limits.uncompressedBytes - totalBytes) {
    throw new Error(`工作区总大小超过 ${formatLimitMB(limits.uncompressedBytes)}，请减少文件后重试`)
  }
}

function formatLimitMB(bytes: number): string {
  return `${Math.floor(bytes / (1024 * 1024))}MB`
}

export function workspaceUploadLimitsFromResponse(value: unknown): WorkspaceUploadLimits {
  if (!value || typeof value !== 'object') return DEFAULT_WORKSPACE_UPLOAD_LIMITS
  const data = value as Record<string, unknown>
  const positive = (key: string, fallback: number) => {
    const candidate = Number(data[key])
    return Number.isFinite(candidate) && candidate > 0 ? candidate : fallback
  }
  return {
    archiveBytes: positive('archive_bytes', DEFAULT_WORKSPACE_UPLOAD_LIMITS.archiveBytes),
    uncompressedBytes: positive('uncompressed_bytes', DEFAULT_WORKSPACE_UPLOAD_LIMITS.uncompressedBytes),
    fileBytes: positive('file_bytes', DEFAULT_WORKSPACE_UPLOAD_LIMITS.fileBytes),
    maxFiles: positive('max_files', DEFAULT_WORKSPACE_UPLOAD_LIMITS.maxFiles),
  }
}

/**
 * Filters a webkitdirectory FileList, strips the shared project folder so the
 * workspace root matches the project root, materializes directory entries,
 * then builds a zip archive for upload as FormData field "archive".
 */
export async function buildWorkspaceZipFromDirectory(
  files: FileList | FilteredFile[],
  limits: WorkspaceUploadLimits = DEFAULT_WORKSPACE_UPLOAD_LIMITS,
): Promise<BuildWorkspaceZipResult> {
  const list = Array.from(files as ArrayLike<FilteredFile>)
  if (list.length === 0) {
    throw new Error('目录为空')
  }

  if (list.length > 1 && list.some((file) => !file.webkitRelativePath)) {
    throw new Error('浏览器没有提供目录相对路径，无法安全保留文件夹结构')
  }

  const rawPaths = list.map((f) => (f.webkitRelativePath || f.name || '').replace(/\\/g, '/'))
  const root = detectCommonRoot(rawPaths)
  const name = root || rawPaths[0]?.split('/')[0] || 'project'
  const isProjectIgnored = await buildProjectIgnoreMatcher(list, root)

  const zip = new JSZip()
  let included = 0
  let skippedHidden = 0
  let skippedLarge = 0
  let skippedOther = 0
  let totalBytes = 0
  const keptRels: string[] = []
  const seenPaths = new Map<string, string>()

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
    if (file.size > limits.fileBytes) {
      skippedLarge++
      continue
    }
    const stripped = stripRootPrefix(raw, root)
    const rel = normalizeArchiveRelativePath(stripped)
    if (!rel || rel.endsWith('/')) {
      skippedOther++
      continue
    }
    if (isProjectIgnored(rel)) {
      skippedOther++
      continue
    }
    if (rel.toLowerCase().endsWith('/.workspace.json') || rel.toLowerCase() === '.workspace.json') {
      throw new Error('目录包含保留文件 .workspace.json，请重命名后重试')
    }
    const pathKey = rel.toLowerCase()
    const previous = seenPaths.get(pathKey)
    if (previous) {
      throw new Error(`存在重复文件路径（忽略大小写）：${previous} 与 ${rel}`)
    }
    assertUploadCapacity(included, totalBytes, file.size, limits)
    seenPaths.set(pathKey, rel)
    zip.file(rel, file)
    keptRels.push(rel)
    included++
    totalBytes += file.size
  }

  if (included === 0) {
    throw new Error(`没有可上传的文件（已过滤凭据、版本库、依赖/构建目录及超过 ${formatLimitMB(limits.fileBytes)} 的文件）`)
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
  if (blob.size > limits.archiveBytes) {
    throw new Error(`压缩包大小超过 ${formatLimitMB(limits.archiveBytes)}，请减少文件后重试`)
  }

  return { blob, name, included, skippedHidden, skippedLarge, skippedOther }
}

export function formatUploadSkipSummary(
  r: BuildWorkspaceZipResult,
  limits: WorkspaceUploadLimits = DEFAULT_WORKSPACE_UPLOAD_LIMITS,
): string {
  const parts: string[] = []
  if (r.skippedHidden > 0) parts.push(`敏感/版本库 ${r.skippedHidden}`)
  if (r.skippedLarge > 0) parts.push(`>${formatLimitMB(limits.fileBytes)} ${r.skippedLarge}`)
  if (r.skippedOther > 0) parts.push(`其它 ${r.skippedOther}`)
  if (parts.length === 0) return ''
  return `，已跳过：${parts.join('、')}`
}
