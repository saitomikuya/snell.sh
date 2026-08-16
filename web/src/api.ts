export type NodeType = 'snell' | 'ss2022' | 'shadowtls'
export interface NodeConfig { version?: string; dns?: string; ipv6?: boolean; tfo?: boolean; public?: boolean; mode?: string; method?: string; obfs?: string; obfsHost?: string; blockMainland?: boolean; sni?: string; wildcardSni?: string }
export interface Node { id:string; type:NodeType; name:string; enabled:boolean; desiredState:string; runtimeVersion:string; listenHost:string; listenPort:number; backendNodeId?:string; config:NodeConfig; revision:number; actualState:string; lastError?:string; createdAt:string; updatedAt:string }
export interface NodeInput { type?:NodeType; name:string; runtimeVersion:string; listenHost:string; listenPort:number; backendNodeId?:string; config:NodeConfig; secret?:string }
export interface Traffic { nodeId:string; period:string; uploadBytes:number; downloadBytes:number; quotaBytes:number; resetDay:number; paused:boolean; pausedByQuota:boolean; updatedAt:string }
export interface Backup { id:string; filename:string; sha256:string; size:number; createdAt:string }
export interface Audit { id:number; action:string; description:string; targetType:string; targetDescription:string; targetId:string; remoteIp:string; details:Record<string,unknown>; detailDescription:string; success:boolean; createdAt:string }
export interface ClientConfig { primary?:string; primaryLabel?:string; surge?:string; surgeLabel?:string; surgeAlternative?:string; surgeAlternativeLabel?:string; surgeNote?:string; clash:string; clashInline?:string; qrCode?:string; qrCodeLabel?:string; protocolLabel:string; importNote?:string; warning?:string; masked:boolean }
export interface UpdateComponent { key:string; name:string; category:'runtime'|'script'; currentVersion:string; latestVersion:string; compatibleVersion?:string; updateAvailable:boolean; compatible:boolean; canApply:boolean; sourceUrl:string; message:string }
export interface UpdateStatus { panelVersion:string; checkedAt:string; compatible:boolean; message:string; components:UpdateComponent[]; adapter:{schemaVersion:number} }

function csrf(): string { return document.cookie.split('; ').find(v => v.startsWith('panel_csrf='))?.split('=')[1] ?? '' }
export class APIError extends Error { constructor(public code:string, message:string, public status:number){super(message)} }
export async function api<T>(path:string, options:RequestInit={}):Promise<T>{
  const method=(options.method ?? 'GET').toUpperCase();
  const headers=new Headers(options.headers);
  if(options.body) headers.set('Content-Type','application/json');
  if(!['GET','HEAD','OPTIONS'].includes(method)) headers.set('X-CSRF-Token',decodeURIComponent(csrf()));
  const response=await fetch('/api/v1'+path,{...options,headers,credentials:'same-origin'});
  if(response.status===204) return undefined as T;
  const body=await response.json().catch(()=>({}));
  if(!response.ok) throw new APIError(body.error?.code ?? 'REQUEST_FAILED',body.error?.message ?? '请求失败',response.status);
  return body as T;
}
