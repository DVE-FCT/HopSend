export function classifyDroppedFiles(paths: string[], uploading: boolean): 'package'|'upload'|'multiple-packages'|'ignore' {
  const packages=paths.filter(path=>/\.hopsend-pair$/i.test(path));
  if(packages.length)return paths.length===1?'package':'multiple-packages';
  return uploading&&paths.length>0?'upload':'ignore';
}

export async function confirmReviewedReply<T>(confirmed:boolean,review:{device:{host_fingerprint:string;ca_sha256:string}}|undefined,ssh:string,ca:string,commit:(ssh:string,ca:string)=>Promise<T>):Promise<T>{
  if(!confirmed||!review)throw new Error('请先通过可信渠道核对两项完整指纹。');
  const expectedSSH=ssh.trim(),expectedCA=ca.trim();
  if(expectedSSH!==review.device.host_fingerprint||expectedCA.toLowerCase()!==review.device.ca_sha256.toLowerCase())throw new Error('填入的可信指纹与回应不一致，尚未导入。');
  return commit(expectedSSH,expectedCA);
}
