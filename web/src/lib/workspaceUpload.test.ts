import { describe, expect, it } from 'vitest'

import {
  collectDirPrefixes,
  detectCommonRoot,
  isHiddenRelativePath,
  shouldSkipRelativePath,
  stripRootPrefix,
} from './workspaceUpload'

describe('workspace upload path handling', () => {
  it('detects hidden files with Unix and Windows separators', () => {
    expect(isHiddenRelativePath('project/.env')).toBe(true)
    expect(isHiddenRelativePath('project\\.git\\config')).toBe(true)
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
})
