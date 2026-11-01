package gateway

import (
 "math"
 "reflect"
 "testing"

 "github.com/niparis/agent-net/internal/protocol"
)

func TestQualityFitRecoversEffects(t *testing.T) {
 var samples []qualitySample
 for i:=0;i<200;i++ { for _,m:=range []string{"a","b"} { for _,h:=range []string{"codex","pi"} { y:=3.0; if m=="a" {y++}; samples=append(samples,qualitySample{Model:m,Harness:h,Effort:"medium",Difficulty:"normal",Value:y,Weight:1}) } } }
 f:=fitQuality(samples)
 if math.Abs(f.effect("model","a")-f.effect("model","b")-1)>.03 {t.Fatalf("model effects: %+v", f)}
 if math.Abs(f.effect("harness","codex"))>.01 || math.Abs(f.effect("harness","pi"))>.01 {t.Fatalf("irrelevant harness: %+v",f)}
}

func TestQualityFitShrinkage(t *testing.T) {
 var samples []qualitySample
 for i:=0;i<100;i++ {samples=append(samples,qualitySample{Model:"common",Harness:"codex",Effort:"medium",Difficulty:"normal",Value:3,Weight:1})}
 samples=append(samples,qualitySample{Model:"rare",Harness:"pi",Effort:"high",Difficulty:"hard",Value:5,Weight:1})
 f:=fitQuality(samples)
 if got:=f.predict("rare","pi","high","hard"); math.Abs(got-f.Average)>.6 {t.Fatalf("one task prediction %g too far from mean %g",got,f.Average)}
}

func TestQualityCacheAndChanges(t *testing.T) {
 s,_:=newTestStore(t)
 if got:=s.QualityAnalysis(); got.Status!="not enough ratings" {t.Fatalf("empty: %+v",got)}
 node,_,_:=s.CreateNode("test","linux")
 caller:=ratingCaller(t,s,node,"caller","gpt-6.1-sol")
 worker:=ratingWorker(t,s,node,caller,"model:high","normal")
 if _,err:=s.AddRating(protocol.RateFrame{RateRequest:protocol.RateRequest{Worker:worker,Score:4},Rater:caller});err!=nil {t.Fatal(err)}
 before:=s.QualityAnalysis()
 if before.Ratings!=1 {t.Fatalf("not refreshed: %+v",before)}
 // Destroy the source behind the cache. Requests must still work and must not fit.
 if _,err:=s.db.Exec(`DROP TABLE worker_ratings`);err!=nil {t.Fatal(err)}
 for i:=0;i<3;i++ {if got:=s.QualityAnalysis();!reflect.DeepEqual(before,got) {t.Fatalf("cache changed: %+v",got)}}
}

func TestQualityChooserOffExactlyUnchanged(t *testing.T) {
 s,_:=newTestStore(t)
 c:=[]protocol.WorkerModelCandidate{{Model:"gpt-6.1-sol",Harness:"codex"},{Model:"gpt-6.1-terra",Harness:"codex"}}
 before:=append([]protocol.WorkerModelCandidate(nil),c...)
 if err:=s.applyQualityWeights(c,"normal");err!=nil {t.Fatal(err)}
 if !reflect.DeepEqual(before,c) {t.Fatalf("off changed candidates: %+v",c)}
 a,b:=&protocol.WorkerRotation{},&protocol.WorkerRotation{}
 for i:=0;i<100;i++ {x,e:=protocol.ChooseWorkerModel(before,"normal",nil,nil,nil,nil,a); if e!=nil {t.Fatal(e)}; y,e:=protocol.ChooseWorkerModel(c,"normal",nil,nil,nil,nil,b);if e!=nil {t.Fatal(e)};if !reflect.DeepEqual(x,y)||!reflect.DeepEqual(a,b) {t.Fatal("off changed chooser")}}
}
