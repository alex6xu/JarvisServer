import { describe, expect, it } from 'vitest'

import {
  assertUploadCapacity,
  buildProjectIgnoreMatcher,
  buildWorkspaceZipFromDirectory,
  collectDirPrefixes,
  detectCommonRoot,
  isHiddenRelativePath,
  isExecutableBinary,
  normalizeArchiveRelativePath,
  MAX_UPLOAD_FILES,
  MAX_UPLOAD_TOTAL_BYTES,
  shouldSkipRelativePath,
  stripRootPrefix,
  workspaceUploadLimitsFromResponse,
} from './workspaceUpload'
import JSZip from 'jszip'

function projectFile(path: string, content: string): File {
  const parts = path.split('/')
  const name = parts[parts.length - 1] || 'file'
  const file = new TextEncoder().encode(content)
  Object.defineProperty(file, 'name', { value: name })
  Object.defineProperty(file, 'size', { value: file.byteLength })
  Object.defineProperty(file, 'text', { value: async () => content })
  Object.defineProperty(file, 'webkitRelativePath', { value: path })
  return file as unknown as File
}

describe('workspace upload path handling', () => {
  it('filters credentials and VCS internals while retaining project dotfiles', () => {
    expect(isHiddenRelativePath('project/.env')).toBe(true)
    expect(isHiddenRelativePath('project\\.git\\config')).toBe(true)
    expect(isHiddenRelativePath('project/.npmrc')).toBe(true)
    expect(isHiddenRelativePath('project/.gitignore')).toBe(false)
    expect(isHiddenRelativePath('project/.github/workflows/ci.yml')).toBe(false)
    expect(isHiddenRelativePath('project/.env.example')).toBe(false)
    expect(isHiddenRelativePath('project/src/main.go')).toBe(false)
  })

  it('filters dependency and platform metadata directories', () => {
    expect(shouldSkipRelativePath('__MACOSX/project/file')).toBe('generated')
    expect(shouldSkipRelativePath('project/.git/config')).toBe('hidden')
    expect(shouldSkipRelativePath('project/.github/workflows/ci.yml')).toBe('hidden')
    expect(shouldSkipRelativePath('project/node_modules/pkg/index.js')).toBe('generated')
    expect(shouldSkipRelativePath('project/vendor/domain.go')).toBe('generated')
    expect(shouldSkipRelativePath('project/dist/index.js')).toBe('generated')
  })

  it('filters compiled files, archives, private configuration and keys', () => {
    expect(shouldSkipRelativePath('server.exe')).toBe('binary')
    expect(shouldSkipRelativePath('lib/native.so')).toBe('binary')
    expect(shouldSkipRelativePath('release.zip')).toBe('binary')
    expect(shouldSkipRelativePath('bundle.min.js')).toBe('generated')
    expect(shouldSkipRelativePath('gateway.test')).toBe('binary')
    expect(shouldSkipRelativePath('web/tsconfig.tsbuildinfo')).toBe('generated')
    expect(shouldSkipRelativePath('config.yaml')).toBe('config')
    expect(shouldSkipRelativePath('etc/gateway.yaml')).toBe('config')
    expect(shouldSkipRelativePath('appsettings.Production.json')).toBe('config')
    expect(shouldSkipRelativePath('server.pem')).toBe('config')
    expect(shouldSkipRelativePath('package.json')).toBeNull()
    expect(shouldSkipRelativePath('tsconfig.json')).toBeNull()
  })

  it('detects an extensionless ELF executable by magic bytes', async () => {
    const binary = new File([new Uint8Array([0x7f, 0x45, 0x4c, 0x46, 0x01])], 'server')
    const source = new File(['package main'], 'main')
    await expect(isExecutableBinary(binary)).resolves.toBe(true)
    await expect(isExecutableBinary(source)).resolves.toBe(false)
  })

  it('honors root and nested .gitignore rules including negation', async () => {
    const files = [
      projectFile('project/generated/.gitignore', '*.tmp\n!keep.tmp\n'),
      projectFile('project/.gitignore', 'dist/\n*.log\n!important.log\n'),
      projectFile('project/dist/bundle.js', 'generated'),
      projectFile('project/debug.log', 'debug'),
      projectFile('project/important.log', 'keep'),
      projectFile('project/generated/drop.tmp', 'drop'),
      projectFile('project/generated/keep.tmp', 'keep'),
    ]
    const matcher = await buildProjectIgnoreMatcher(files, 'project')
    expect(matcher('dist/bundle.js')).toBe(true)
    expect(matcher('debug.log')).toBe(true)
    expect(matcher('important.log')).toBe(false)
    expect(matcher('generated/drop.tmp')).toBe(true)
    expect(matcher('generated/keep.tmp')).toBe(false)
    expect(matcher('.gitignore')).toBe(false)
  })

  it('strips only a common directory root', () => {
    const paths = ['project/src/main.go', 'project/README.md']
    expect(detectCommonRoot(paths)).toBe('project')
    expect(stripRootPrefix(paths[0], 'project')).toBe('src/main.go')
    expect(detectCommonRoot(['one/a.txt', 'two/b.txt'])).toBe('')
    expect(detectCommonRoot(['README.md', 'project/main.go'])).toBe('')
  })

  it('collects unique parent directories in stable order', () => {
    expect(collectDirPrefixes(['src/app/main.ts', 'src/lib/util.ts', 'README.md'])).toEqual([
      'src',
      'src/app',
      'src/lib',
    ])
  })

  it('rejects unsafe archive paths', () => {
    expect(() => normalizeArchiveRelativePath('../secret')).toThrow('无效的文件路径')
    expect(() => normalizeArchiveRelativePath('C:/secret')).toThrow('无效的文件路径')
    expect(normalizeArchiveRelativePath('.github/workflows/ci.yml')).toBe('.github/workflows/ci.yml')
  })

  it('rejects file count and total size overflow instead of truncating', () => {
    expect(() => assertUploadCapacity(MAX_UPLOAD_FILES - 1, 0, 1)).not.toThrow()
    expect(() => assertUploadCapacity(MAX_UPLOAD_FILES, 0, 1)).toThrow(`${MAX_UPLOAD_FILES}`)
    expect(() => assertUploadCapacity(1, MAX_UPLOAD_TOTAL_BYTES - 1, 2)).toThrow('100MB')
  })

  it('uses server upload limits with safe defaults for invalid values', () => {
    expect(workspaceUploadLimitsFromResponse({
      archive_bytes: 700,
      uncompressed_bytes: 900,
      file_bytes: 100,
      max_files: 12,
    })).toEqual({ archiveBytes: 700, uncompressedBytes: 900, fileBytes: 100, maxFiles: 12 })
    expect(workspaceUploadLimitsFromResponse({ archive_bytes: -1 }).archiveBytes).toBe(MAX_UPLOAD_TOTAL_BYTES)
  })

  it('builds a zip that keeps project metadata and excludes credentials', async () => {
    const result = await buildWorkspaceZipFromDirectory([
      projectFile('project/src/main.ts', 'main'),
      projectFile('project/.gitignore', 'node_modules\ngenerated/\n'),
      projectFile('project/.github/workflows/ci.yml', 'name: ci'),
      projectFile('project/generated/output.js', 'generated'),
      projectFile('project/.env', 'TOKEN=secret'),
      projectFile('project/.git/config', 'private'),
    ])

    expect(result.included).toBe(2)
    expect(result.skippedHidden).toBe(3)
    const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
    expect(Object.keys(zip.files)).toContain('src/main.ts')
    expect(Object.keys(zip.files)).toContain('.gitignore')
    expect(Object.keys(zip.files)).not.toContain('.github/workflows/ci.yml')
    expect(Object.keys(zip.files)).not.toContain('generated/output.js')
    expect(Object.keys(zip.files)).not.toContain('.env')
    expect(Object.keys(zip.files)).not.toContain('.git/config')
  })

  it('skips a file over the configured per-file limit', async () => {
    const result = await buildWorkspaceZipFromDirectory([
      projectFile('project/main.go', 'ok'),
      projectFile('project/large.dat', '1234'),
    ], {
      archiveBytes: 1024 * 1024,
      uncompressedBytes: 100,
      fileBytes: 3,
      maxFiles: 10,
    })
    expect(result.included).toBe(1)
    expect(result.skippedLarge).toBe(1)
  })

  it('rejects missing relative paths and case-insensitive collisions', async () => {
    await expect(buildWorkspaceZipFromDirectory([
      new File(['a'], 'a.txt'),
      new File(['b'], 'b.txt'),
    ])).rejects.toThrow('目录相对路径')

    await expect(buildWorkspaceZipFromDirectory([
      projectFile('project/src/Main.ts', 'a'),
      projectFile('project/src/main.ts', 'b'),
    ])).rejects.toThrow('重复文件路径')
  })
})
