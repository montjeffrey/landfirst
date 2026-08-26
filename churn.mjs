import { standardRepo, commit, checkout, checkoutNew, inRepo, cleanupAll } from './test/fixtures.mjs';
import { openRepo, buildModel, cleanupScratch } from './src/core.mjs';
const dir = standardRepo();
for (let i=0;i<5;i++){ checkout(dir,'main'); commit(dir,{'trunk.txt':'t '+i+'\n'},'trunk '+i); }
checkout(dir,'main');
const m = await inRepo(dir, () => buildModel(openRepo(), {limit:200,maxPairs:50},{probes:false}));
const bad = m.commits.filter(c => c.lane === undefined || Number.isNaN(c.lane));
console.log('commits: ' + m.commits.length + '  undefined lane: ' + bad.length);
bad.slice(0,5).forEach(c => console.log('   ' + c.short + ' ' + c.subject.slice(0,30) + ' refs=' + JSON.stringify(c.refs)));
console.log('base: ' + m.base);
console.log('graphBranchNames: ' + JSON.stringify(m.graphBranchNames));
const tip = m.commits.find(c => (c.refs||[]).some(r => r===m.base || r.endsWith('-> '+m.base)));
console.log('base tip: ' + (tip ? tip.short + ' lane=' + tip.lane : 'NOT IN GRAPH'));
console.log('lane histogram: ' + JSON.stringify(m.commits.reduce((a,c)=>{a[c.lane]=(a[c.lane]||0)+1;return a;},{})));
cleanupScratch(); cleanupAll();
