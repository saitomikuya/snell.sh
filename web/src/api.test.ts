import { describe, expect, it } from 'vitest'
import type { NodeConfig } from './api'

describe('frontend schema',()=>{it('represents the default SS-2022 mode',()=>{const config:NodeConfig={mode:'tcp_and_udp',method:'2022-blake3-aes-128-gcm'};expect(config.mode).toBe('tcp_and_udp')})})
