import type {BrowseError, BrowsePageResult, BrowseSharesResult, RemoteEntry, RemotePage, RemoteShare} from './bridge';

export interface RemoteBrowserState {
  device: string;
  shares: RemoteShare[];
  share: string;
  path: string;
  entries: RemoteEntry[];
  cursor: string;
  hasMore: boolean;
  busy: boolean;
  loaded: boolean;
  error?: BrowseError;
  observedAt: string;
}

export const newBrowserState = (): RemoteBrowserState => ({
  device:'', shares:[], share:'', path:'.', entries:[], cursor:'', hasMore:false,
  busy:false, loaded:false, observedAt:'',
});

export const readable = (share: RemoteShare) => share.access === 'read_only' || share.access === 'read_write';
export const childPath = (parent: string, name: string) => parent === '.' ? name : `${parent}/${name}`;
export const parentPath = (path: string) => path === '.' || !path.includes('/') ? '.' : path.slice(0,path.lastIndexOf('/'));
const safePath = (path: string) => path === '.' || (!!path && path.length <= 4096 && !/[\\:\0]/.test(path) && path.split('/').every(p=>p!==''&&p!=='.'&&p!=='..'));
const encoder = new TextEncoder();
function compareNames(a: string, b: string) {
  const x=encoder.encode(a),y=encoder.encode(b);
  for(let i=0;i<Math.min(x.length,y.length);i++)if(x[i]!==y[i])return x[i]-y[i];
  return x.length-y.length;
}

export function mergeRemotePage(existing: RemoteEntry[], page: RemotePage, share: string, path: string, cursor: string): RemoteEntry[] {
  if(page.share_id!==share||page.path!==path||!Array.isArray(page.entries)||page.entries.length>100||page.has_more!==(page.next_cursor!=='')||page.next_cursor.length>4096||(page.has_more&&(!page.entries.length||page.next_cursor===cursor)))throw new Error('目录分页响应范围或游标无效，未拼接列表。');
  if(existing.length+page.entries.length>100_000)throw new Error('已达 100000 项浏览上限，请拆分子目录。');
  let last=existing.at(-1)?.name;
  for(const entry of page.entries){
    if(!entry.name||entry.name.includes('/')||!safePath(entry.name)||entry.name==='.'||!Number.isSafeInteger(entry.size_bytes)||entry.size_bytes<0||(entry.directory&&entry.size_bytes!==0)||(last!==undefined&&compareNames(last,entry.name)>=0))throw new Error('目录项目重复、倒序或内容无效，请重新读取。');
    last=entry.name;
  }
  return [...existing,...page.entries];
}

interface BrowserAPI {
  BrowseDeviceShares(id: string): Promise<BrowseSharesResult>;
  BrowseDeviceEntries(id: string, share: string, path: string, cursor: string, limit: number): Promise<BrowsePageResult>;
}

// A generation belongs to one device/share/path request. Late network results
// cannot overwrite a newer selection, close, refresh or trust configuration.
export function createRemoteBrowser(state: RemoteBrowserState, api: BrowserAPI) {
  let generation=0,disposed=false;
  const current=(epoch:number)=>!disposed&&epoch===generation;
  const clear=()=>{state.entries=[];state.cursor='';state.hasMore=false;state.loaded=false;state.observedAt='';state.error=undefined;};
  const failed=(error:BrowseError)=>{clear();state.error=error;};
  async function page(epoch:number,cursor=''){
    state.busy=true;
    try{
      const value=await api.BrowseDeviceEntries(state.device,state.share,state.path,cursor,100);
      if(!current(epoch))return;
      if(value.error){failed(value.error);return;}
      if(!value.page)throw new Error('Agent 未返回目录页。');
      state.entries=mergeRemotePage(cursor?state.entries:[],value.page,state.share,state.path,cursor);
      state.cursor=value.page.next_cursor;state.hasMore=value.page.has_more;
      state.loaded=true;state.observedAt=value.observed_at;state.error=undefined;
    }catch(error){if(current(epoch))failed({code:'browse_failed',message:String(error)});}
    finally{if(current(epoch))state.busy=false;}
  }
  return {
    async device(id:string,preferredShare=''){
      const epoch=++generation;
      Object.assign(state,newBrowserState(),{device:id});
      if(!id||disposed)return;
      state.busy=true;
      try{
        const value=await api.BrowseDeviceShares(id);
        if(!current(epoch))return;
        if(value.error){failed(value.error);return;}
        const seen=new Set<string>();
        if(!Array.isArray(value.shares))throw new Error('共享列表无效。');
        for(const share of value.shares){
          if(!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(share.id)||seen.has(share.id)||(!readable(share)&&share.access!=='write_only'))throw new Error('共享列表无效。');
          seen.add(share.id);
        }
        state.shares=value.shares;
        const share=value.shares.find(s=>s.id===preferredShare&&readable(s))||value.shares.find(readable);
        state.share=share?.id||value.shares[0]?.id||'';
        if(!share){state.error={code:'browse_denied',message:value.shares.length?'当前共享只有上传权限，不能读取目录。':'当前设备没有授权共享。'};return;}
        await page(epoch);
      }catch(error){if(current(epoch))failed({code:'browse_failed',message:String(error)});}
      finally{if(current(epoch))state.busy=false;}
    },
    async share(id:string){
      const epoch=++generation;clear();state.share=id;state.path='.';state.busy=false;
      if(!state.shares.some(s=>s.id===id&&readable(s))){failed({code:'browse_denied',message:'此共享没有目录读取权限。'});return;}
      await page(epoch);
    },
    async navigate(path:string){
      const epoch=++generation;clear();state.busy=false;
      if(!safePath(path)){failed({code:'invalid_path',message:'只能浏览授权共享内的相对目录。'});return;}
      state.path=path;
      if(!state.share||!state.shares.some(s=>s.id===state.share&&readable(s)))return;
      await page(epoch);
    },
    async next(){if(!state.busy&&state.hasMore&&state.cursor)await page(generation,state.cursor);},
    async reload(){if(state.share)await this.navigate(state.path);else await this.device(state.device);},
    invalidate(){generation++;state.busy=false;clear();},
    dispose(){disposed=true;generation++;state.busy=false;},
  };
}
