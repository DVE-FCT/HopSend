import {test} from 'node:test';
import assert from 'node:assert/strict';
import {classifyDroppedFiles,confirmReviewedReply} from '../../apps/desktop/frontend/src/package-flow.ts';

test('pair package has priority over upload and mixed drops are rejected',()=>{
 assert.equal(classifyDroppedFiles(['C:/source.HOPSEND-PAIR'],true),'package');
 assert.equal(classifyDroppedFiles(['C:/source.hopsend-pair','C:/data.bin'],true),'multiple-packages');
 assert.equal(classifyDroppedFiles(['C:/ordinary.zip'],true),'upload');
 assert.equal(classifyDroppedFiles(['C:/ordinary.zip'],false),'ignore');
});
test('unconfirmed or mismatched pins never invoke the import backend',async()=>{
 let calls=0;const commit=async()=>{calls++;};
 const review={device:{host_fingerprint:'SHA256:verified',ca_sha256:'abcdef'}};
 await assert.rejects(confirmReviewedReply(false,review,'SHA256:verified','abcdef',commit));
 await assert.rejects(confirmReviewedReply(true,undefined,'SHA256:verified','abcdef',commit));
 await assert.rejects(confirmReviewedReply(true,review,'SHA256:wrong','abcdef',commit));
 await assert.rejects(confirmReviewedReply(true,review,'SHA256:verified','different',commit));
 assert.equal(calls,0);
});
test('explicit trusted pin confirmation invokes import once with normalized input',async()=>{
 let calls=0;const review={device:{host_fingerprint:'SHA256:verified',ca_sha256:'abcdef'}};
 const result=await confirmReviewedReply(true,review,' SHA256:verified ',' ABCDEF ',async(ssh,ca)=>{calls++;assert.equal(ssh,'SHA256:verified');assert.equal(ca,'ABCDEF');return 'connection.json';});
 assert.equal(calls,1);assert.equal(result,'connection.json');
});
