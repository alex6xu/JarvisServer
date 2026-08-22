export interface SSEFrames {
  payloads: string[]
  remainder: string
}

export function splitSSEFrames(buffer: string): SSEFrames {
  const frames = buffer.split(/\r?\n\r?\n/)
  const remainder = frames.pop() || ''
  const payloads: string[] = []
  for (const frame of frames) {
    const data = frame
      .split(/\r?\n/)
      .filter((line) => line.startsWith('data:'))
      .map((line) => line.replace(/^data:\s?/, ''))
      .join('\n')
    if (data) payloads.push(data)
  }
  return { payloads, remainder }
}
