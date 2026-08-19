import { describe, expect, it } from 'vitest'

import {
  assertUploadCapacity,
  buildWorkspaceZipFromDirectory,
  collectDirPrefixes,
  detectCommonRoot,
  isHiddenRelativePath,
  normalizeArchiveRelativePath,
  MAX_UPLOAD_FILES,
  MAX_UPLOAD_TOTAL_BYTES,
  shouldSkipRelativePath,
  stripRootPrefix,
} from './workspaceUpload'
import JSZip from 'jszip'

function projectFile(path: string, content: string): File {
  const parts = path.split('/')
  const name = parts[parts.length - 1] || 'file'
  const file = new TextEncoder().encode(content)
  Object.defineProperty(file, 'name', { value: name })
  Object.defineProperty(file, 'size', { value: file.byteLength })
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
    expect(shouldSkipRelativePath('project/node_modules/pkg/index.js')).toBe('vendor')
    expect(shouldSkipRelativePath('__MACOSX/project/file')).toBe('vendor')
    expect(shouldSkipRelativePath('project/.git/config')).toBe('hidden')
    expect(shouldSkipRelativePath('project/vendor/domain.go')).toBeNull()
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

  it('builds a zip that keeps project metadata and excludes credentials', async () => {
    const result = await buildWorkspaceZipFromDirectory([
      projectFile('project/src/main.ts', 'main'),
      projectFile('project/.gitignore', 'node_modules'),
      projectFile('project/.github/workflows/ci.yml', 'name: ci'),
      projectFile('project/.env', 'TOKEN=secret'),
      projectFile('project/.git/config', 'private'),
    ])

    expect(result.included).toBe(3)
    expect(result.skippedHidden).toBe(2)
    const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
    expect(Object.keys(zip.files)).toContain('src/main.ts')
    expect(Object.keys(zip.files)).toContain('.gitignore')
    expect(Object.keys(zip.files)).toContain('.github/workflows/ci.yml')
    expect(Object.keys(zip.files)).not.toContain('.env')
    expect(Object.keys(zip.files)).not.toContain('.git/config')
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
