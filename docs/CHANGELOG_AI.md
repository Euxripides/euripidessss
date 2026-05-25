### 2026-05-25 当前

#### 本次任务
- 修复点击线条后明细数据为空的问题：后端 queryEdgeRows 只匹配主列，当实体名来自备用列时（如 交易账号 而非 交易户名）匹配不到任何行。

#### 新增功能
- 边缘明细数据现在能正确匹配通过备用列（交易账号/交易户名/交易方身份证号/对手卡号/对手户名等）解析的实体名称。
- 新增 lowColumnMapping 结构体和 lowColumnMappingFromPayload 函数，统一管理列映射提取。
- matchesDateRange 时间过滤逻辑增加了 
ormalizeFilterBoundary 精确时间边界处理。

#### 修改文件
- internal/api/handlers.go
- docs/AI_HANDOFF.md
- docs/CHANGELOG_AI.md

#### 接口变化
- 无新增/删除/重命名端点路径。
- /api/flow/edge-detail/imported 请求体新增可选字段：source_account_column, source_name_column, source_id_column, source_label_column, 	arget_card_column, 	arget_name_column, 	arget_id_column, 	arget_label_column。

#### 数据库变化
- 无。

#### 前端变化
- 无。

#### 验证结果
- go build -o bin\etl-server.exe .\cmd\server\ 通过
- go test ./internal/... — 全部 29 个测试通过
- cd frontend; npm run build — TypeScript + Vite 构建通过

#### 未完成/待确认
- 需要重启 8000 端口的 etl-server.exe 使修复生效。
- 用户需要在实际图中点击边缘验证明细数据显示正常。

### 2026-05-24 23:34

#### 鏈浠诲姟
- 琛ラ綈娴佸悜鍥惧瓧娈垫槧灏勫脊绐椾腑鐨?`浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞`銆?- 璁╄繖浜涘瓧娈靛湪宸叉槧灏勬椂鍑虹幇鍦ㄥ彸渚х瓫閫夊尯锛屾湭鏄犲皠鏃朵笉鏄剧ず銆?- 灏嗘椂闂寸瓫閫夋敼涓轰腑鏂囧崰浣嶆枃鏈紝骞舵敮鎸佺簿纭埌灏忔椂銆佸垎閽熴€佺銆?- 灏嗘祦鍚戝浘妯℃澘鏇挎崲涓虹敤鎴蜂笂浼犵殑 `D:\app\妗岄潰\娴佸悜鍥炬暟鎹ā鏉?xlsx`銆?- 瀵规暟鎹瓫閫夊仛绔埌绔璁℃祴璇曪紝瑕嗙洊褰掍竴鍖栥€佺瓫閫夈€佸缓鍥捐仛鍚堝拰涓讳綋鏀舵敮缁熻銆?
#### 鏂板鍔熻兘
- 鏂板鏄庣粏瀛楁绛涢€夛細`浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞`銆?- `/api/flow/build` 鏀寔璇诲彇鍜岀瓫閫夋槧灏勫悗鐨勬槑缁嗗瓧娈点€?- 鍚庣绛涢€夌幇鍦ㄥ悓鏃跺簲鐢ㄤ氦鏄撴柟鏍囩銆佸鎵嬫爣绛俱€佹槑缁嗗瓧娈点€佹柟鍚戝拰绮剧‘鏃堕棿鑼冨洿銆?- 鏂板鍚庣瀹¤娴嬭瘯锛屾壒閲忕敓鎴愬璐﹀彿銆佸瀵规墜銆佸鏂瑰悜銆佸鏃堕棿銆佸閲戦娴嬭瘯鏁版嵁锛屽苟鏍稿绛涢€夊悗琛屾暟銆侀噾棰濄€佽竟鑱氬悎鍜屼富浣撴祦鍏ユ祦鍑虹粺璁°€?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/flowTypes.ts`
- `frontend/src/features/flow/flowMapping.ts`
- `frontend/src/features/flow/FlowMappingModal.tsx`
- `frontend/src/features/flow/FlowFieldFilters.tsx`
- `frontend/src/features/flow/useFlowFilters.ts`
- `frontend/src/features/flow/FlowBuildControls.tsx`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowGraphWorkspace.tsx`
- `frontend/src/features/flow/FlowInspectorPanel.tsx`
- `frontend/src/features/flow/flowApi.ts`
- `frontend/src/hooks/useFlowOperations.ts`
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `tmp/flow_template.xlsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炪€佸垹闄ゆ垨閲嶅懡鍚嶆帴鍙ｈ矾寰勩€?- `/api/flow/build` 鏂板鍙€夎姹傚瓧娈碉細`serial_column`銆乣summary_column`銆乣remark_column`銆乣detail_filters`銆?- `/api/flow/template` 涓嬭浇鍐呭鏇存柊涓?15 鍒楁ā鏉匡紝鏂板 `浜ゆ槗娴佹按鍙穈銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 瀛楁鏄犲皠寮圭獥鏂板 `浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞` 涓夎銆?- 鍙充晶绛涢€夊尯鏂板鏄庣粏瀛楁閫夋嫨鍣紝鍙湁瀛楁宸叉槧灏勬垨鑳借嚜鍔ㄨВ鏋愭椂鎵嶆樉绀哄搴旂瓫閫夐」銆?- 鏃堕棿鑼冨洿閫夋嫨鍣ㄥ崰浣嶇鏀逛负 `寮€濮嬫椂闂碻銆乣缁撴潫鏃堕棿`锛屾樉绀烘牸寮忎负 `YYYY-MM-DD HH:mm:ss`銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 閫氳繃銆?- `go test ./internal/api -run TestFlowFilterEndToEndAuditMatchesGraphAggregates -count=1 -v` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go test ./internal/...` 閫氳繃銆?- `go vet ./internal/...` 閫氳繃銆?- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\` 閫氳繃銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 宸查噸鍚?8000 鏈嶅姟锛宍http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- 宸蹭笅杞藉苟妫€鏌?`http://127.0.0.1:8000/api/flow/template`锛岃〃澶翠负 `浜ゆ槗鏂规埛鍚? 浜ゆ槗鏂硅处鎴? 浜ゆ槗鏂硅韩浠借瘉鍙? 浜ゆ槗鏂规爣绛? 浜ゆ槗鏃堕棿, 浜ゆ槗閲戦, 鏀朵粯鏍囧織, 浜ゆ槗浣欓, 浜ゆ槗瀵规墜璐﹀崱鍙? 瀵规墜鎴峰悕, 瀵规墜韬唤璇佸彿, 瀵规墜鏍囩, 浜ゆ槗娴佹按鍙? 鎽樿璇存槑, 澶囨敞`銆?- 宸茬‘璁ら椤靛紩鐢ㄥ綋鍓嶆瀯寤轰骇鐗?`assets/index-Dg-VWM7A.js` 涓?`assets/index-B-imr4oU.css`銆?
#### 鏈畬鎴?寰呯‘璁?- 濡傛灉娴忚鍣ㄧ紦瀛樹簡鏃?JS 璧勬簮锛岄渶瑕佸己鍒跺埛鏂伴〉闈㈠悗娴嬭瘯銆?
### 2026-05-24 23:02

#### 鏈浠诲姟
- 淇鏁版嵁搴撳鍏ュ璞″尯浠嶇劧鏄剧ず鍦ㄥ乏渚ф爲涓嬫柟鐨勯棶棰樸€?
#### 鏂板鍔熻兘
- 鏃狅紝鏈涓?CSS 甯冨眬淇銆?
#### 淇敼鏂囦欢
- `frontend/src/styles/shared.css`
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鍒犻櫎 `frontend/src/styles/shared.css` 鏈熬娈嬬暀鐨勬湭闂悎 `@media` 鍧椼€?- 淇 `db-import.css` 琚敊璇寘杩涘獟浣撴煡璇㈢殑闂銆?- 妗岄潰瀹藉害涓嬫暟鎹簱瀵煎叆寮圭獥鎭㈠涓哄乏渚ф爲銆佸彸渚у璞″尯鐨勫乏鍙冲垎鏍忓竷灞€銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- 宸茬‘璁ゆ瀯寤哄悗鐨?CSS 涓?`.db-import-shell` 浣嶄簬椤跺眰锛屼笖鍖呭惈 `display:grid` 鍜屼袱鏍?`grid-template-columns`銆?- 宸叉壂鎻忔湰娆?touched 婧愮爜鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?- `http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- `http://127.0.0.1:8000` 宸插紩鐢ㄥ綋鍓嶆瀯寤轰骇鐗?`index-B-imr4oU.css` 鍜?`index-DTwUX0_S.js`銆?
#### 鏈畬鎴?寰呯‘璁?- 濡傛灉娴忚鍣ㄧ紦瀛樹簡鏃ц祫婧愶紝闇€瑕佸己鍒跺埛鏂伴〉闈㈠悗鍐嶇湅甯冨眬銆?
### 2026-05-24 22:44

#### 鏈浠诲姟
- 淇澶ч噺鏁版嵁瀵煎叆鍚?Flow 鐢熸垚鍥惧崱椤裤€佺粺璁″紓甯搞€佷富浣撶瓫閫夊悗鍑虹幇瀛ょ珛璐﹀彿涓旀病鏈夎繛绾跨殑闂銆?- 閲嶇偣淇瀹¤鍦烘櫙锛氶€夋嫨涓€涓氦鏄撴柟璐﹀彿銆佹敹浠樻爣蹇椾负鈥滃嚭鈥濄€佷笉閫夋嫨瀵规墜淇℃伅鏃讹紝搴旂粺璁″苟灞曠ず璇ヨ处鍙锋墍鏈夊尮閰嶇殑娴佸嚭浜ゆ槗瀵规墜鍏崇郴銆?
#### 鏂板鍔熻兘
- `/api/flow/build` 鏀寔鍙€?`max_edges`锛屽墠绔湪鏈変氦鏄撴柟/瀵规墜绛涢€夌殑瀹¤鏋勫浘鍦烘櫙璇锋眰 5000 鏉″叧绯讳笂闄愶紝鍚庣涔熶細瀵逛富鍔ㄧ瓫閫夊満鏅娇鐢?5000 鐨勫璁′笂闄愩€?- Flow graph meta 鏂板 `rendered_edges`銆乣rendered_nodes`锛岀敤浜庡尯鍒嗗叏閲忚仛鍚堣妯″拰褰撳墠瀹為檯娓叉煋瑙勬ā銆?
#### 淇敼鏂囦欢
- `internal/etl/flow_graph.go`
- `internal/etl/etl_test.go`
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `frontend/src/features/flow/useFlowGraph.ts`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `frontend/src/features/flow/useFlowFilters.ts`
- `frontend/src/features/flow/FlowGraphFilters.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炴垨鍒犻櫎鎺ュ彛璺緞銆?- `/api/flow/build` 鏂板鍙€夎姹傚瓧娈?`max_edges`銆?- `/api/flow/build` 鍝嶅簲 `meta` 鏂板 `rendered_edges`銆乣rendered_nodes`銆?- `meta.total_nodes` 淇涓烘湭鎴柇鑱氬悎鍥剧殑鑺傜偣鎬绘暟锛屼笉鍐嶄娇鐢ㄦ埅鏂悗杈归泦鍚堢殑鑺傜偣鏁般€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏂板浘灞傜敓鎴?鏇挎崲鍚庝細娓呯┖鏃х殑涓讳綋绛涢€夈€侀噾棰濈瓫閫夈€佽矾寰勮拷韪拰閫変腑鍏崇郴锛岄伩鍏嶆棫鍥剧姸鎬佹薄鏌撴柊鍥俱€?- 閲戦婊戝潡鎸夊綋鍓嶅浘鏈€澶ч噾棰濋挸鍒舵樉绀哄拰杩囨护锛岄伩鍏嶆棫鐨勫ぇ棰濋槇鍊兼妸鏂板浘鎵€鏈夎竟杩囨护鎺夈€?- 閲戦/鏃堕棿/娓叉煋杩囨护鐢熸晥鏃讹紝鐢诲竷鍙繚鐣欎粛鏈夊叧鑱旇竟鐨勮妭鐐癸紝涓嶅啀鏄剧ず鏃犺繛绾跨殑瀛ょ珛璐﹀彿銆?- 鏈変氦鏄撴柟鎴栧鎵嬬瓫閫夋椂锛屾瀯鍥?payload 鍙戦€?`max_edges: 5000`锛涙棤涓讳綋绛涢€夌殑鎬昏鏋勫浘鍙戦€?`max_edges: 600`銆?
#### 楠岃瘉缁撴灉
- `go test ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go vet ./internal/...` 閫氳繃銆?- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\` 閫氳繃銆?- 宸查噸寤?`bin\etl-server.exe` 骞堕噸鍚?8000 鏈嶅姟锛宍http://127.0.0.1:8000/api/health` 杩斿洖 `ok`銆?- 宸叉壂鎻忔湰娆?touched Flow/鍚庣鏂囦欢鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈鏈敤鐢ㄦ埛鐨?520k 琛屽師濮嬫暟鎹仛娴忚鍣ㄧ澶嶇幇銆?- 鏃犵瓫閫夌殑澶у浘鎬昏浠嶄繚鐣?600 鏉℃渶楂橀噾棰濊仛鍚堝叧绯荤殑娓叉煋涓婇檺锛涘璁℃槑缁嗗簲閫氳繃浜ゆ槗鏂?瀵规墜绛涢€夎繘鍏?5000 涓婇檺銆?- 褰撳墠鍙祴璇曞湴鍧€锛歚http://127.0.0.1:8000`锛涢獙璇佹椂鍚庣 PID 涓?`37172`銆?
# CHANGELOG_AI.md

### 2026-05-24 22:29

#### 鏈浠诲姟
- 淇鏁版嵁搴撳鍏ュ脊绐椾腑瀵硅薄鍒嗙被鐨勪綅缃細瀵硅薄鍒嗙被搴斿湪鍙充晶瀵硅薄鍖猴紝涓嶅簲鎸傚湪宸︿晶妯″紡鑺傜偣涓嬮潰銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈涓哄竷灞€淇銆?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 宸︿晶鏍戞敼涓鸿繛鎺?-> 鏁版嵁搴?-> 妯″紡 -> 琛紝涓嶅啀鍦ㄦā寮忎笅鏄剧ず鈥滆〃/瑙嗗浘/瀹炰綋鍖栬鍥?鍑芥暟/鏌ヨ/澶囦唤鈥濆垎绫昏妭鐐广€?- 鍙充晶鈥滃璞♀€濋〉鏂板瀵硅薄鍒嗙被鎸夐挳锛氳〃銆佽鍥俱€佸疄浣撳寲瑙嗗浘銆佸嚱鏁般€佹煡璇€佸浠姐€?- 琛ㄥ璞″垪琛ㄤ繚鐣欏湪鍙充晶锛屽弻鍑昏〃浠嶄細鎵撳紑琛ㄦ暟鎹〉銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- 宸叉悳绱?`frontend/src/features/flow/DBImportModal.tsx` 鍜?`frontend/src/features/flow/db-import.css`锛岀‘璁ゅ乏渚?`tables:` 鍒嗙被鑺傜偣宸茬Щ闄ゃ€?- 宸叉壂鎻?`frontend/src/features/flow/DBImportModal.tsx`銆乣frontend/src/features/flow/db-import.css` 鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 瑙嗗浘銆佸疄浣撳寲瑙嗗浘銆佸嚱鏁般€佹煡璇€佸浠藉垎绫诲綋鍓嶄粛涓虹鐢ㄥ睍绀洪」锛屽緟鍚庣鏀寔瀵瑰簲鍏冩暟鎹帴鍙ｅ悗鍙惎鐢ㄣ€?
### 2026-05-24 22:19

#### 鏈浠诲姟
- 璋冩暣鏁版嵁搴撳鍏ュ脊绐楃殑杩炴帴娴嬭瘯鎻愮ず銆佹爲褰㈢粨鏋勫拰鏁翠綋甯冨眬锛屼娇鍏舵洿鎺ヨ繎鐢ㄦ埛鎻愪緵鐨勬暟鎹簱瀹㈡埛绔埅鍥俱€?
#### 鏂板鍔熻兘
- 鈥滄祴璇曡繛鎺モ€濇垚鍔熸垨澶辫触鏃舵樉绀洪€氱煡妗嗭紝鎴愬姛灞曠ず杩炴帴鐩爣锛屽け璐ュ睍绀洪敊璇師鍥犮€?- 鏂板杩炴帴 -> 鏁版嵁搴?-> 妯″紡 -> 瀵硅薄鍒嗙粍 -> 琛ㄧ殑鏍戝舰瀵艰埅缁撴瀯銆?- 鏂板鈥滃璞♀€濅富瑙嗗浘锛屽彸渚т互鈥滃悕 / 琛?/ 娉ㄩ噴鈥濊〃鏍煎睍绀哄綋鍓嶆ā寮忎笅鐨勮〃銆?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏁版嵁搴撳鍏ュ脊绐楀乏渚т粠骞抽摵鍒楄〃鏀逛负 Ant Design Tree銆?- 鍙充晶鏂板绫讳技鏁版嵁搴撳鎴风鐨勫璞″伐鍏锋爮锛氭墦寮€琛ㄣ€佽璁¤〃銆佹柊寤鸿〃銆佸垹闄よ〃銆佸鍏ュ悜瀵笺€佸鍑哄悜瀵笺€?- 鎵撳紑琛ㄥ悗鍒囨崲鍒拌〃鏁版嵁椤碉紱閫夋嫨妯″紡鍚庨粯璁ゅ睍绀哄璞￠〉銆?- 鏂板缓琛ㄣ€佸垹闄よ〃銆佸鍑哄悜瀵煎綋鍓嶄粎浣滀负甯冨眬鍗犱綅涓旂鐢紝鏈柊澧?DDL 鎴栧鍑烘帴鍙ｃ€?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `cd E:\codex\etl; go test ./internal/...` 閫氳繃銆?- 宸叉壂鎻?`frontend/src/features/flow/DBImportModal.tsx`銆乣frontend/src/features/flow/db-import.css` 鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 褰撳墠琛ㄥ垪琛ㄦ帴鍙ｅ彧杩斿洖鍚嶇О鍜岀被鍨嬶紝鍙充晶鈥滆 / 娉ㄩ噴鈥濇殏涓虹┖鍗犱綅锛涘闇€鐪熷疄琛屾暟/娉ㄩ噴锛岄渶瑕佹墿灞曞悗绔厓鏁版嵁鎺ュ彛銆?
### 2026-05-24 21:46

#### 鏈浠诲姟
- 鍚姩椤圭洰锛屼緵鐢ㄦ埛娴嬭瘯褰撳墠鏁版嵁搴撳鍏ョ増鏈€?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呭惎鍔?閲嶅惎鏈嶅姟銆?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- 宸叉鏌?8000 绔彛鍘熸湁杩涚▼鍙婂懡浠よ銆?- 宸插仠姝㈡棫鐨?`E:\codex\etl\bin\etl-server.exe` 杩涚▼銆?- 宸蹭粠 `E:\codex\etl` 鍚姩褰撳墠 `bin\etl-server.exe`銆?- `http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- `http://127.0.0.1:8000/api/db/connections` 杩斿洖 JSON锛岀‘璁ゆ暟鎹簱瀵煎叆 API 宸插湪 8000 鍙敤銆?- `http://127.0.0.1:8000` 杩斿洖 HTTP 200锛屽苟鍔犺浇褰撳墠鍓嶇鏋勫缓璧勬簮銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€傚綋鍓嶅彲娴嬭瘯鍦板潃涓?`http://127.0.0.1:8000`銆?
### 2026-05-24 20:58

#### 鏈浠诲姟
- 浣跨敤鐢ㄦ埛鎻愪緵鐨勬湰鏈?MySQL 杩炴帴鍋氭暟鎹簱瀵煎叆鍔熻兘鐪熷疄鎺ュ彛娴嬭瘯銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呮祴璇曢獙璇併€?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 涓存椂鍒涘缓 MySQL database `codex_mysql_import_test` 鍜岃〃 `flow_txn`銆?- 娴嬭瘯缁撴潫鍚庡凡鍒犻櫎涓存椂 database銆?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- MySQL 8.0.39 杩炴帴鎴愬姛銆?- `/api/db/connections` 杩炴帴淇濆瓨銆佸垪琛ㄨ鍙栥€佸瘑鐮侀殣钘忋€佸垹闄ら€氳繃銆?- `/api/db/connections/:id/test` 閫氳繃銆?- 鏁版嵁搴撱€乻chema銆佽〃銆佸瓧娈靛厓鏁版嵁璇诲彇閫氳繃銆?- `/api/db/preview` 鍒嗛〉棰勮閫氳繃锛岃繑鍥?2 琛屽苟鏍囪鎴柇銆?- `/api/db/search` 鎼滅储閫氳繃锛岃繑鍥?1 琛屻€?- `/api/db/query` SELECT 鏌ヨ閫氳繃锛岄潪 SELECT 鏌ヨ鎸夐鏈熻鎷︽埅銆?- `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete` 鍧囬€氳繃锛屽悇褰卞搷 1 琛屻€?- `/api/db/mappings/auto` 鑷姩鏄犲皠閫氳繃锛屽繀濉瓧娈靛潎宸插尮閰嶃€?- `/api/db/mappings/confirm` 鏄犲皠淇濆瓨閫氳繃銆?- `/api/db/import/tasks` 鍒涘缓鍜?`/api/db/import/tasks/:id/start` 鎵ц閫氳繃锛屽鍏?3 琛屾垚鍔熴€? 琛屽け璐ャ€?- `/api/flow/build` 鍩轰簬鏁版嵁搴撳鍏?session 鐢熸垚娴佸悜鍥鹃€氳繃锛岃繑鍥?3 涓妭鐐广€? 鏉¤竟銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€備复鏃?MySQL database銆佷复鏃?flow session銆佹祴璇曡繛鎺ラ厤缃拰涓存椂 8001 鏈嶅姟鍧囧凡娓呯悊銆?- 8000 绔彛鏈噸鍚紱鏈娴嬭瘯浣跨敤涓存椂 `PORT=8001` 褰撳墠浜岃繘鍒跺畬鎴愩€?
### 2026-05-24 18:55

#### 鏈浠诲姟
- 浣跨敤鐢ㄦ埛鎻愪緵鐨勬湰鏈?PostgreSQL 杩炴帴鍋氭暟鎹簱瀵煎叆鍔熻兘鐪熷疄鎺ュ彛娴嬭瘯銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呮祴璇曢獙璇併€?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 涓存椂鍒涘缓 PostgreSQL schema `codex_dbimport_test` 鍜岃〃 `flow_txn`銆?- 娴嬭瘯缁撴潫鍚庡凡鍒犻櫎涓存椂 schema銆?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- PostgreSQL 17 杩炴帴鎴愬姛銆?- `/api/db/connections` 杩炴帴淇濆瓨銆佸垪琛ㄨ鍙栥€佸瘑鐮侀殣钘忋€佸垹闄ら€氳繃銆?- `/api/db/connections/:id/test` 閫氳繃銆?- 鏁版嵁搴撱€乻chema銆佽〃銆佸瓧娈靛厓鏁版嵁璇诲彇閫氳繃銆?- `/api/db/preview` 鍒嗛〉棰勮閫氳繃锛岃繑鍥?2 琛屽苟鏍囪鎴柇銆?- `/api/db/search` 鎼滅储閫氳繃锛岃繑鍥?1 琛屻€?- `/api/db/query` SELECT 鏌ヨ閫氳繃锛岄潪 SELECT 鏌ヨ鎸夐鏈熻鎷︽埅銆?- `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete` 鍧囬€氳繃锛屽悇褰卞搷 1 琛屻€?- `/api/db/mappings/auto` 鑷姩鏄犲皠閫氳繃锛屽繀濉瓧娈靛潎宸插尮閰嶃€?- `/api/db/mappings/confirm` 鏄犲皠淇濆瓨閫氳繃銆?- `/api/db/import/tasks` 鍒涘缓鍜?`/api/db/import/tasks/:id/start` 鎵ц閫氳繃锛屽鍏?3 琛屾垚鍔熴€? 琛屽け璐ャ€?- `/api/flow/build` 鍩轰簬鏁版嵁搴撳鍏?session 鐢熸垚娴佸悜鍥鹃€氳繃锛岃繑鍥?3 涓妭鐐广€? 鏉¤竟銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈寤鸿〃浣跨敤 ASCII 瀛楁鍚嶏紝鍥犱负 PowerShell 璋冪敤 `psql -c` 鍒涘缓涓枃鏍囪瘑绗﹂亣鍒板鎴风缂栫爜闂锛涘闇€楠岃瘉涓枃鏁版嵁搴撳瓧娈靛悕锛屽簲浣跨敤 UTF-8 閰嶇疆姝ｇ‘鐨?SQL 瀹㈡埛绔垨浠庡簲鐢?UI 鍒涘缓/閫夋嫨宸叉湁涓枃瀛楁琛ㄧ户缁祴璇曘€?- 8000 绔彛杩愯鐨勬槸杈冩棫浜岃繘鍒讹紝鏈噸鍚紱鏈娴嬭瘯浣跨敤涓存椂 `PORT=8001` 褰撳墠浜岃繘鍒跺畬鎴愶紝娴嬭瘯鍚庡凡鍋滄銆?
### 2026-05-24 18:28

#### 鏈浠诲姟
- 鎸夋暟鎹簱瀵煎叆鍔熻兘鏀归€犻渶姹傚畬鎴愬墿浣欏悗绔€佸墠绔€佹祴璇曞拰浜や粯鏂囨。銆?
#### 鏂板鍔熻兘
- 鏂板鏁版嵁搴撳鍏ュ叆鍙ｏ紝鏀寔 MySQL/PostgreSQL 杩炴帴閰嶇疆銆佹祴璇曘€佹祻瑙堛€侀瑙堛€佹悳绱€佹煡璇€佸瓧娈垫槧灏勭‘璁ゃ€佹槧灏勪繚瀛樺拰瀵煎叆娴佸悜鍥俱€?- 鏂板瀹夊叏琛ㄧ紪杈戞帴鍙ｅ拰鍓嶇缂栬緫椤碉細鏂板銆佷慨鏀广€佸垹闄ゅ潎璧板弬鏁板寲鎺ュ彛锛屼慨鏀?鍒犻櫎蹇呴』鎻愪緵涓婚敭鎴栧敮涓€鏉′欢銆?- 鏂板鏁版嵁搴撳鍏ヤ换鍔°€侀敊璇褰曞拰鎶ュ憡鎺ュ彛銆?- 鏂板鏈湴 AES-GCM 鍔犲瘑閰嶇疆瀛樺偍锛屽瘑鐮佷粎鍦ㄧ敤鎴峰嬀閫変繚瀛樺瘑鐮佹椂鍐欏叆鍔犲瘑鏂囦欢銆?
#### 淇敼鏂囦欢
- `.gitignore`
- `go.mod`
- `go.sum`
- `internal/api/handlers.go`
- `internal/api/db_handlers.go`
- `internal/dbimport/types.go`
- `internal/dbimport/store.go`
- `internal/dbimport/service.go`
- `internal/dbimport/service_test.go`
- `frontend/src/App.tsx`
- `frontend/src/hooks/useFlowOperations.ts`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowSourceModal.tsx`
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/dbImportApi.ts`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- `鏁版嵁搴撳鍏ュ姛鑳芥敼閫犲畬鎴愭姤鍛?md`

#### 鎺ュ彛鍙樺寲
- 鏂板 `/api/db/connections` 杩炴帴绠＄悊鎺ュ彛銆?- 鏂板 `/api/db/connections/:id/databases|schemas|tables|columns|indexes` 鍏冩暟鎹帴鍙ｃ€?- 鏂板 `/api/db/preview`銆乣/api/db/search`銆乣/api/db/query`銆乣/api/db/query/cancel`銆?- 鏂板 `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete`銆?- 鏂板 `/api/db/mappings`銆乣/api/db/mappings/auto`銆乣/api/db/mappings/confirm`銆?- 鏂板 `/api/db/import/tasks` 鍙婁换鍔?start/cancel/errors/report 鎺ュ彛銆?- 鏈慨鏀规棦鏈?`/api/flow/*`銆乣/api/process` 璺緞銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犲閮ㄦ暟鎹簱渚濊禆銆?- 鏂板鏈湴鍔犲瘑閰嶇疆鏂囦欢鐩綍 `backend/data/db_import/`锛屽凡鍔犲叆 `.gitignore`銆?
#### 鍓嶇鍙樺寲
- 鏁版嵁鏉ユ簮寮圭獥鍒犻櫎鍙鐨勨€滄竻娲楃殑鏂囦欢鈥濆叆鍙ｃ€?- 鏂板鈥滄暟鎹簱瀵煎叆鈥濆崱鐗囧拰鏁版嵁搴撳鍏ュ脊绐椼€?- 鏁版嵁搴撳脊绐楀寘鍚繛鎺ュ垪琛ㄣ€佹暟鎹簱/schema/琛ㄦ祻瑙堛€佸垎椤甸瑙堛€佽〃缁撴瀯銆丼ELECT 鏌ヨ銆佹暟鎹紪杈戙€佸瓧娈垫槧灏勩€佸鍏ヤ换鍔￠〉銆?
#### 楠岃瘉缁撴灉
- `go test ./internal/...` 閫氳繃銆?- `go vet ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 涓存椂 `PORT=8001` 鍚姩浜岃繘鍒讹紝`/api/health` 鍜?`/api/db/connections` 閫氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈繛鎺ョ湡瀹?MySQL/PostgreSQL 瀹炰緥鍋氶泦鎴愭祴璇曪紝闇€鐢ㄦ埛鎻愪緵鍙敤鏁版嵁搴撹处鍙峰悗楠岃瘉杩炴帴銆佸厓鏁版嵁銆侀瑙堝拰瀵煎叆銆?- 绗竴鐗堝鍏?UI 绛夊緟 start 璇锋眰瀹屾垚锛涘悗绔凡鎸夐〉淇濆瓨杩涘害骞舵敮鎸?cancel 鐘舵€侊紝鍚庣画鍙敼鎴愬墠绔疆璇㈠悗鍙颁换鍔°€?
### 2026-05-24 16:17

#### 鏈浠诲姟
- 妫€鏌ュ苟淇椤圭洰閲嶆瀯鍚庣殑 Flow 鍥剧浉鍏?bug銆?
#### 鏂板鍔熻兘
- 鏂板鍚庣 API 鍗曞厓娴嬭瘯锛岃鐩?Flow 绛涢€夊拰鏂瑰悜褰掍竴鍖栥€?
#### 淇敼鏂囦欢
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳帴鍙ｈ矾寰勫彉鍖栥€?- `/api/flow/history` 鎵╁睍杩斿洖 `job_id`銆乣name`銆乣size`銆乣updated_at`銆乣status`銆?- `/api/flow/history/:job_id` 鏀逛负杩斿洖鍙仮澶嶅鍏ユ暟鎹泦鐨勫瓧娈碉細`session_id`銆乣job_id`銆乣name`銆乣rows`銆乣columns`銆乣files`銆乣sample`銆乣signature`銆乣mapping_rule`銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鍘嗗彶鏁版嵁鍔犺浇涓嶅啀鍋囪鍚庣涓€瀹氳繑鍥?`flow_graph`锛屽彲鎭㈠鍘嗗彶瀵煎叆鏁版嵁骞剁户缁敓鎴愬浘銆?- 鏅鸿兘鍒嗘瀽鍦ㄥ崰浣?API 涓嶈繑鍥?`flow_graph` 鏃跺彧灞曠ず鎶ュ憡锛屼笉鍐嶈Е鍙戠┖ graph 宕╂簝銆?- 鐢熸垚鍥剧户缁吋瀹归《灞?`nodes/edges/meta` 鍜屽祵濂?`flow_graph` 涓ょ鍝嶅簲褰㈢姸銆?
#### 鍚庣鍙樺寲
- 鍘嗗彶鍒楄〃/璇︽儏涓庡墠绔?`HistoryItem`銆乣ImportedDataset` 鏁版嵁褰㈢姸瀵归綈銆?- Flow 鏋勫浘绛涢€夋敮鎸佺洰鏍囧瓧娈电瓫閫夈€佹柟鍚戠瓫閫夈€佸紑濮?缁撴潫鏃ユ湡绛涢€夈€?- 鏋勫浘鍜屾湭鐭ユ柟鍚戞鏌ユ敮鎸佸唴缃柟鍚戝綊涓€鍖栦笌鎸佷箙鍖栨柟鍚戝埆鍚嶃€?
#### 楠岃瘉缁撴灉
- `go vet ./internal/...` 閫氳繃銆?- `go test -count=1 -timeout 60s ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 涓存椂 `PORT=8001` 鍚姩鏂颁簩杩涘埗锛宍/api/health`銆乣/api/flow/history`銆乣/api/flow/history/70027426-b61` 鍧囬€氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 8000 绔彛宸叉湁 `E:\codex\etl\bin\etl-server.exe` 姝ｅ湪杩愯涓斿仴搴锋鏌ユ甯革紝鏈鏈噸鍚杩涚▼銆?
### 2026-05-24 16:01

#### 鏈浠诲姟
- 淇鐐瑰嚮鈥滅敓鎴愬浘鈥濆悗鍓嶇鎶ラ敊 `Cannot read properties of undefined (reading 'meta')`銆?
#### 鏂板鍔熻兘
- 鏂板鍓嶇 Flow 鍥惧搷搴斿綊涓€鍖栭€昏緫锛屽吋瀹?`/api/flow/build` 鐨勯《灞?`nodes/edges/meta` 鍝嶅簲鍜屽祵濂?`flow_graph` 鍝嶅簲銆?
#### 淇敼鏂囦欢
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炴垨鍙樻洿鎺ュ彛銆?- 鏈慨鏀瑰悗绔?`/api/flow/build` 鍝嶅簲锛屼粎澧炲己鍓嶇鍏煎璇诲彇銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鐢熸垚鍥炬祦绋嬫敼涓轰娇鐢ㄥ綊涓€鍖栧悗鐨?graph 瀵硅薄璇诲彇 `meta` 骞舵覆鏌撹妭鐐?杈广€?- 寮傚父鎴栫┖鍥?payload 浼氳繘鍏ュ凡鏈夌┖鏁版嵁鎻愮ず锛屼笉鍐嶇洿鎺ユ姏 JavaScript 杩愯鏃堕敊璇€?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃銆?- `cd E:\codex\etl; go test ./internal/...` 閫氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€?
鐢ㄤ簬璁板綍 AI/Codex/Hermes 姣忔瀹屾垚鐨勫姛鑳藉彉鏇淬€?

## 璁板綍鏍煎紡

### YYYY-MM-DD HH:mm

#### 鏈浠诲姟
-

#### 鏂板鍔熻兘
-

#### 淇敼鏂囦欢
-

#### 鎺ュ彛鍙樺寲
-

#### 鏁版嵁搴撳彉鍖?
-

#### 鍓嶇鍙樺寲
-
#### 楠岃瘉缁撴灉
-

#### 鏈畬鎴?寰呯‘璁?-

### 2026-05-25 00:01

#### 鏈浠诲姟
- 淇鍥捐氨瀵煎嚭鍙崟鑾疯鍙ｈ寖鍥村唴鑺傜偣锛屾敼涓烘崟鑾风敾甯冨唴鍏ㄩ儴鑺傜偣

#### 鏂板鍔熻兘
- 鍦?`flowExport.ts` 涓坊鍔?`expandForFullCapture` 鍑芥暟锛氭崟鑾峰墠鍏堣绠楁墍鏈夎妭鐐圭殑鍖呭洿鐩掞紝涓存椂鎵╁睍 ReactFlow 瀹瑰櫒灏哄骞堕噸瀹氫綅瑙嗗彛锛屼娇 html-to-image 瀹屾暣娓叉煋鏁村紶鍥?- PNG/JPEG/WebP/SVG 鍗曞浘瀵煎嚭鐜板湪鍖呭惈鐢诲竷鍐呮墍鏈夎妭鐐瑰拰杈癸紝涓嶉檺浜庡彲瑙佽鍙?- ZIP 鎵撳寘涓殑 `.svg` 鍜?`.png` 鏂囦欢鍚屾牱浣跨敤鍏ㄥ浘鎹曡幏

#### 淇敼鏂囦欢
- `frontend/src/features/flow/flowExport.ts`

#### 鎺ュ彛鍙樺寲
- 鏃?
#### 鏁版嵁搴撳彉鍖?- 鏃?
#### 鍓嶇鍙樺寲
- `captureCanvasRaster` 鍜?`captureCanvasSvg` 鏀逛负鍏堣皟鐢?`expandForFullCapture` 鍐嶆崟鑾凤紝鎹曡幏鍚庤嚜鍔ㄦ仮澶嶅師濮嬫牱寮?- `expandForFullCapture` 璁＄畻鎵€鏈?`.react-flow__node` 鍏冪礌鐨勫寘鍥寸洅锛屼复鏃惰缃?`overflow: visible` 鍜屾墿灞曞昂瀵革紝骞跺亸绉昏鍙ｅ彉鎹?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 鈥?TypeScript + Vite 鏋勫缓閫氳繃
- `go test ./internal/...` 鈥?29 涓?Go 娴嬭瘯鍏ㄩ儴閫氳繃

### 2026-05-25 02:21

#### 本次任务
- 修复字段映射阶段已选择 `交易流水号`、`摘要说明`、`备注` 后，右侧数据筛选区没有自动显示对应明细筛选框的问题。
- 补齐后端 Flow 明细字段映射、过滤和边明细备用列匹配，恢复 Go API 测试基线。

#### 新增功能
- 映射可解析到 `交易流水号`、`摘要说明`、`备注` 时，右侧筛选区自动显示对应明细筛选行。
- `/api/flow/build` 会把映射后的流水号、摘要说明、备注写入归一化交易行，并应用 `detail_filters`。
- 边明细查询支持源端/目标端备用列匹配，适配图节点来自账号、户名、证件号等不同映射字段的场景。
- 流向图模板兜底生成列补齐 `交易流水号`。

#### 修改文件
- `frontend/src/features/flow/useFlowFilters.ts`
- `internal/api/handlers.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口路径。
- `/api/flow/build` 继续支持可选 `serial_column`、`summary_column`、`remark_column`、`detail_filters`。
- `/api/flow/edge-detail/imported` 继续支持备用列字段。

#### 数据库变化
- 无。

#### 前端变化
- 字段映射确认后，已映射的明细字段会自动补入右侧筛选框，不再需要用户二次添加明细筛选字段。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 已重启 `E:\codex\etl\bin\etl-server.exe`，`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 引用当前构建产物 `assets/index-K4UkElxG.js` 和 `assets/index-B-imr4oU.css`。

#### 未完成/待确认
- 浏览器如缓存旧资源，需要强制刷新后再验证右侧筛选区。
- 工作区已有多处先前未提交改动及 `backend/config/custom_rules.json` 修改，本次未回退。
