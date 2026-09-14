import { BookOpen, Bug, Hammer, ScanLine, Terminal } from 'lucide-react'

const suggestions = [
  { icon: BookOpen, color: '#2585e6', label: '探索并理解代码', prompt: '请帮我梳理项目结构，解释核心模块及它们之间的关系。' },
  { icon: Hammer, color: '#9456df', label: '构建新功能、应用或工具', prompt: '我想为项目新增一个功能，请先和我确认需求并制定实现方案。' },
  { icon: ScanLine, color: '#19985a', label: '审查代码并提出修改建议', prompt: '请审查项目代码，重点检查潜在问题、边界条件和可维护性。' },
  { icon: Bug, color: '#e57532', label: '修复问题和失败', prompt: '请帮我定位并修复一个问题，我会提供现象、日志和复现步骤。' },
]

export default function WorkbenchWelcome({ projectName, onSelect }: { projectName?: string; onSelect: (prompt: string) => void }) {
  return (
    <div className="workbench-welcome">
      <div className="workbench-welcome-content">
        <Terminal className="workbench-mark" size={48} strokeWidth={1.4} aria-hidden="true" />
        <h2>{projectName ? <>你想在 <span>{projectName}</span> 中构建什么？</> : '今天，想一起完成什么？'}</h2>
        <div className="workbench-suggestions">
          {suggestions.map(({ icon: Icon, color, label, prompt }) => <button key={label} type="button" onClick={() => onSelect(prompt)}><Icon size={20} strokeWidth={1.6} style={{ color }} /><span>{label}</span></button>)}
        </div>
      </div>
    </div>
  )
}
