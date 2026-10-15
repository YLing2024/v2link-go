import { describe, expect, it } from 'vitest'
import { displayAlias, vlessLink } from './vless'

// vless:// 构造（对齐 server 端格式）；host/path 可注入，测试直接传参不依赖环境变量
describe('vlessLink', () => {
  it('生成标准 vless ws+tls 链接', () => {
    const uri = vlessLink('11111111-1111-4111-8111-111111111111', 'v2.example.com', '/v2ws', undefined, '')
    expect(uri).toContain('vless://11111111-1111-4111-8111-111111111111@')
    expect(uri).toContain(':443?')
    expect(uri).toContain('encryption=none')
    expect(uri).toContain('type=ws')
    expect(uri).toContain('security=tls')
    expect(uri).toContain('path=%2Fv2ws')
    expect(uri).toContain(`host=${encodeURIComponent('v2.example.com')}`)
  })

  it('host 由调用方注入；path 自动补 /', () => {
    const uri = vlessLink('u', 'node.example.com', 'x', undefined, '')
    expect(uri.startsWith('vless://u@node.example.com:443?')).toBe(true)
    expect(uri).toContain('host=node.example.com')
    expect(uri).toContain('path=%2Fx')
  })

  it('传别名 → 追加百分号编码的 #fragment', () => {
    const uri = vlessLink('u', 'h.example.com', '/p', '我的节点')
    expect(uri.endsWith(`#${encodeURIComponent('我的节点')}`)).toBe(true)
  })

  it('别名空/空白 → 不带 #', () => {
    expect(vlessLink('u', 'h.example.com', '/p')).not.toContain('#')
    expect(vlessLink('u', 'h.example.com', '/p', '   ')).not.toContain('#')
  })
})

describe('displayAlias（回退链 alias → note → id）', () => {
  it('依次回退', () => {
    expect(displayAlias({ alias: 'A', note: 'N', id: 'I' })).toBe('A')
    expect(displayAlias({ alias: '', note: 'N', id: 'I' })).toBe('N')
    expect(displayAlias({ alias: ' ', note: '', id: 'I' })).toBe('I')
  })
})
