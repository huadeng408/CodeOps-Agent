package session

import "strings"

// Only this fixed standard-library program executes beside hidden assertions.
// Candidate source is archive data and is never imported by the grader.
var evaluationGraderProgram = strings.ReplaceAll(`package main

import (
 "archive/tar"
 "bytes"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "io/fs"
 "math"
 "math/big"
 "os"
 "path"
 "regexp"
 "strconv"
 "strings"
 "unicode"
 "unicode/utf8"
)

type inputFile struct {
 Path string __TAG__json:"path"__TAG__
 Exists bool __TAG__json:"exists"__TAG__
 SHA256 string __TAG__json:"sha256"__TAG__
 Size int64 __TAG__json:"size"__TAG__
 Mode uint32 __TAG__json:"mode"__TAG__
}
type candidateManifest struct {
 SchemaVersion int __TAG__json:"schema_version"__TAG__
 CandidateSHA256 string __TAG__json:"candidate_sha256"__TAG__
 CandidateArchiveSHA256 string __TAG__json:"candidate_archive_sha256"__TAG__
 Files []inputFile __TAG__json:"files"__TAG__
}
type inputPart struct {
 Path string __TAG__json:"path"__TAG__
 SHA256 string __TAG__json:"sha256"__TAG__
 Size int64 __TAG__json:"size"__TAG__
}
type outcome struct {
 SchemaVersion int __TAG__json:"schema_version"__TAG__
 Valid bool __TAG__json:"valid"__TAG__
 Passed *bool __TAG__json:"passed"__TAG__
 ChecksTotal *int __TAG__json:"checks_total"__TAG__
 ChecksPassed *int __TAG__json:"checks_passed"__TAG__
 CandidateSHA256 string __TAG__json:"candidate_sha256"__TAG__
 CandidateArchiveSHA256 string __TAG__json:"candidate_archive_sha256"__TAG__
 InputSHA256 string __TAG__json:"input_sha256"__TAG__
 CheckerSHA256 string __TAG__json:"checker_sha256"__TAG__
 ChecksSHA256 string __TAG__json:"checks_sha256"__TAG__
 ErrorCode string __TAG__json:"error_code"__TAG__
}
func hash(data []byte) string { sum:=sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func read(name string, limit int64) ([]byte,error) {
 f,err:=os.Open(name);if err!=nil{return nil,err};defer f.Close()
 b,err:=io.ReadAll(io.LimitReader(f,limit+1));if err!=nil||int64(len(b))>limit{return nil,fmt.Errorf("bounded input unavailable")};return b,nil
}
func safePath(name string) bool {
 if !utf8.ValidString(name)||!fs.ValidPath(name)||name=="."||path.Clean(name)!=name||strings.ContainsAny(name,"\\:*?\"<>|")||strings.IndexFunc(name,unicode.IsControl)>=0{return false}
 for _,part:=range strings.Split(name,"/") {
  if strings.TrimRight(part,". ")!=part||strings.EqualFold(part,".git"){return false}
  base:=strings.ToUpper(strings.SplitN(part,".",2)[0]);serial:=strings.TrimPrefix(strings.TrimPrefix(base,"COM"),"LPT")
  if base=="CON"||base=="PRN"||base=="AUX"||base=="NUL"||base=="CONIN$"||base=="CONOUT$"||base=="CLOCK$"||((strings.HasPrefix(base,"COM")||strings.HasPrefix(base,"LPT"))&&len([]rune(serial))==1&&strings.Contains("0123456789¹²³",serial)){return false}
 };return true
}
func jsonValue(d *json.Decoder) (any,error) {
 token,err:=d.Token();if err!=nil{return nil,err}
 if delimiter,ok:=token.(json.Delim);ok {
  switch delimiter {
  case '{':
   result:=map[string]any{};for d.More(){key,err:=d.Token();if err!=nil{return nil,err};name,ok:=key.(string);if !ok{return nil,fmt.Errorf("invalid object")};if _,exists:=result[name];exists{return nil,fmt.Errorf("duplicate key")};value,err:=jsonValue(d);if err!=nil{return nil,err};result[name]=value};end,err:=d.Token();if err!=nil||end!=json.Delim('}'){return nil,fmt.Errorf("invalid object")};return result,nil
  case '[':
   result:=[]any{};for d.More(){value,err:=jsonValue(d);if err!=nil{return nil,err};result=append(result,value)};end,err:=d.Token();if err!=nil||end!=json.Delim(']'){return nil,fmt.Errorf("invalid array")};return result,nil
  };return nil,fmt.Errorf("invalid delimiter")
 }
 if n,ok:=token.(json.Number);ok&&strings.ContainsAny(string(n),".eE"){v,err:=strconv.ParseFloat(string(n),64);if err!=nil||math.IsNaN(v)||math.IsInf(v,0){return nil,fmt.Errorf("nonfinite number")}}
 return token,nil
}
func parse(data []byte) (any,error) {
 if !utf8.Valid(data){return nil,fmt.Errorf("invalid utf8")};d:=json.NewDecoder(bytes.NewReader(data));d.UseNumber();value,err:=jsonValue(d);if err!=nil{return nil,err};if _,err=d.Token();err!=io.EOF{return nil,fmt.Errorf("trailing json")};return value,nil
}
func equal(a,b any) bool {
 switch a:=a.(type) {
 case nil:return b==nil
 case bool:v,ok:=b.(bool);return ok&&a==v
 case string:v,ok:=b.(string);return ok&&a==v
 case json.Number:
  v,ok:=b.(json.Number);if !ok{return false};af,bf:=strings.ContainsAny(string(a),".eE"),strings.ContainsAny(string(v),".eE");if af!=bf{return false}
  if af{x,e1:=strconv.ParseFloat(string(a),64);y,e2:=strconv.ParseFloat(string(v),64);return e1==nil&&e2==nil&&math.Float64bits(x)==math.Float64bits(y)}
  x,ok1:=new(big.Int).SetString(string(a),10);y,ok2:=new(big.Int).SetString(string(v),10);return ok1&&ok2&&x.Cmp(y)==0
 case []any:v,ok:=b.([]any);if !ok||len(a)!=len(v){return false};for i:=range a{if !equal(a[i],v[i]){return false}};return true
 case map[string]any:v,ok:=b.(map[string]any);if !ok||len(a)!=len(v){return false};for key,x:=range a{y,exists:=v[key];if !exists||!equal(x,y){return false}};return true
 };return false
}
func grade() (out outcome) {
 out.SchemaVersion=1
 var parts []inputPart
 data:=map[string][]byte{}
 for _,name:=range []string{"candidate.json","candidate.tar","checks.json","grader.go"} {
  if name=="candidate.tar" {
   f,err:=os.Open(name);if err!=nil{out.ErrorCode="invalid_input";return};h:=sha256.New();n,err:=io.Copy(h,io.LimitReader(f,320<<20+1));f.Close();if err!=nil||n>320<<20{out.ErrorCode="invalid_input";return};parts=append(parts,inputPart{name,hex.EncodeToString(h.Sum(nil)),n});out.CandidateArchiveSHA256=parts[len(parts)-1].SHA256;continue
  }
  limit:=int64(8<<20);if name=="checks.json"{limit=1<<20};b,err:=read(name,limit);if err!=nil{out.ErrorCode="invalid_input";return};data[name]=b;parts=append(parts,inputPart{name,hash(b),int64(len(b))})
 }
 encoded,_:=json.Marshal(parts);out.InputSHA256=hash(encoded);out.CheckerSHA256=hash(data["grader.go"]);out.ChecksSHA256=hash(data["checks.json"])
 var candidate candidateManifest
 decoder:=json.NewDecoder(bytes.NewReader(data["candidate.json"]));decoder.DisallowUnknownFields()
 if decoder.Decode(&candidate)!=nil||decoder.Decode(new(any))!=io.EOF||candidate.SchemaVersion!=1||candidate.CandidateArchiveSHA256!=out.CandidateArchiveSHA256||candidate.Files==nil||len(candidate.Files)>20000{out.ErrorCode="invalid_candidate";return}
 encoded,_=json.Marshal(candidate.Files);out.CandidateSHA256=hash(encoded);if out.CandidateSHA256!=candidate.CandidateSHA256{out.ErrorCode="invalid_candidate";return}
 expected:=map[string]inputFile{};previous:="";aliases:=map[string]bool{}
 for _,file:=range candidate.Files {
  if !safePath(file.Path)||file.Path<=previous||aliases[strings.ToLower(file.Path)]||file.Size<0||file.Size>8<<20||file.Mode>0777{out.ErrorCode="invalid_candidate";return};previous=file.Path;aliases[strings.ToLower(file.Path)]=true;expected[file.Path]=file
 }
 raw,err:=parse(data["checks.json"]);checks,ok:=raw.(map[string]any)
 if err!=nil||!ok||len(checks)!=2||checks["schema_version"]!=json.Number("1"){out.ErrorCode="invalid_checks";return}
 list,ok:=checks["files"].([]any);if !ok||len(list)==0||len(list)>256{out.ErrorCode="invalid_checks";return}
 needed:=map[string]bool{}
 for _,raw:=range list {
  check,ok:=raw.(map[string]any);if !ok||len(check)!=2{out.ErrorCode="invalid_checks";return};name,ok:=check["path"].(string);if !ok||!safePath(name){out.ErrorCode="invalid_checks";return}
  kinds:=0
  if v,exists:=check["contains"];exists{kinds++;text,ok:=v.(string);if !ok||text==""{out.ErrorCode="invalid_checks";return}}
  if _,exists:=check["json_equals"];exists{kinds++}
  if v,exists:=check["sha256"];exists{kinds++;pin,ok:=v.(string);if !ok||!regexp.MustCompile("^[a-fA-F0-9]{64}$").MatchString(pin){out.ErrorCode="invalid_checks";return}}
  if kinds!=1{out.ErrorCode="invalid_checks";return};needed[name]=true
 }
 archive,err:=os.Open("candidate.tar");if err!=nil{out.ErrorCode="invalid_candidate";return};defer archive.Close();reader:=tar.NewReader(archive)
 observed:=map[string]bool{};contents:=map[string][]byte{};hashes:=map[string]string{};var total int64
 for {
  header,err:=reader.Next();if err==io.EOF{break};if err!=nil{out.ErrorCode="invalid_candidate";return}
  name:=strings.TrimSuffix(header.Name,"/");if !safePath(name){out.ErrorCode="invalid_candidate";return};if header.Typeflag==tar.TypeDir{continue}
  file,exists:=expected[name];if !exists||!file.Exists||observed[name]||header.Typeflag!=tar.TypeReg||header.Size!=file.Size{out.ErrorCode="invalid_candidate";return}
  b,err:=io.ReadAll(io.LimitReader(reader,8<<20+1));total+=int64(len(b));if err!=nil||int64(len(b))!=file.Size||total>256<<20||hash(b)!=file.SHA256{out.ErrorCode="invalid_candidate";return};observed[name]=true;hashes[name]=file.SHA256;if needed[name]{contents[name]=b}
 }
 for name,file:=range expected{if observed[name]!=file.Exists{out.ErrorCode="invalid_candidate";return}}
 passed:=0
 for _,raw:=range list {
  check:=raw.(map[string]any);name:=check["path"].(string);b,exists:=contents[name];matched:=false
  if exists {if v,ok:=check["contains"];ok{matched=utf8.Valid(b)&&strings.Contains(string(b),v.(string))};if v,ok:=check["sha256"];ok{matched=hashes[name]==strings.ToLower(v.(string))};if v,ok:=check["json_equals"];ok{actual,err:=parse(b);matched=err==nil&&equal(actual,v)}}
  if matched{passed++}
 }
 count:=len(list);all:=passed==count;out.Valid=true;out.Passed=&all;out.ChecksTotal=&count;out.ChecksPassed=&passed;return
}
func main(){_ = json.NewEncoder(os.Stdout).Encode(grade())}
`, "__TAG__", "`")
