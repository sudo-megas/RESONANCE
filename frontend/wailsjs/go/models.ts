export namespace main {
	
	export class ActivityEntry {
	    kind: string;
	    appName: string;
	    summary: string;
	    timestamp: string;
	
	    static createFrom(source: any = {}) {
	        return new ActivityEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.appName = source["appName"];
	        this.summary = source["summary"];
	        this.timestamp = source["timestamp"];
	    }
	}
	export class TrackedDir {
	    path: string;
	    fileCount: number;
	    system: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TrackedDir(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.fileCount = source["fileCount"];
	        this.system = source["system"];
	    }
	}
	export class AppComposition {
	    name: string;
	    files: string[];
	    dirs: TrackedDir[];
	
	    static createFrom(source: any = {}) {
	        return new AppComposition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.files = source["files"];
	        this.dirs = this.convertValues(source["dirs"], TrackedDir);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileRow {
	    path: string;
	    system: boolean;
	    state: string;
	    sourceModified: string;
	    vaultModified: string;
	    size: number;
	    sourceSize: number;
	
	    static createFrom(source: any = {}) {
	        return new FileRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.system = source["system"];
	        this.state = source["state"];
	        this.sourceModified = source["sourceModified"];
	        this.vaultModified = source["vaultModified"];
	        this.size = source["size"];
	        this.sourceSize = source["sourceSize"];
	    }
	}
	export class AppRow {
	    name: string;
	    files: FileRow[];
	    drifted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.files = this.convertValues(source["files"], FileRow);
	        this.drifted = source["drifted"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileContents {
	    text: string;
	    binary: boolean;
	    tooLarge: boolean;
	    missing: boolean;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new FileContents(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.binary = source["binary"];
	        this.tooLarge = source["tooLarge"];
	        this.missing = source["missing"];
	        this.size = source["size"];
	    }
	}
	export class DiffPair {
	    live: FileContents;
	    vault: FileContents;
	
	    static createFrom(source: any = {}) {
	        return new DiffPair(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.live = this.convertValues(source["live"], FileContents);
	        this.vault = this.convertValues(source["vault"], FileContents);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class MachineInfo {
	    kernel: string;
	    os: string;
	    hostname: string;
	    username: string;
	
	    static createFrom(source: any = {}) {
	        return new MachineInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kernel = source["kernel"];
	        this.os = source["os"];
	        this.hostname = source["hostname"];
	        this.username = source["username"];
	    }
	}
	export class OrphanReport {
	    files: string[];
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new OrphanReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.bytes = source["bytes"];
	    }
	}
	export class PathPreview {
	    fileCount: number;
	    folderCount: number;
	    folders: string[];
	    systemFolderCount: number;
	    systemFileCount: number;
	
	    static createFrom(source: any = {}) {
	        return new PathPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileCount = source["fileCount"];
	        this.folderCount = source["folderCount"];
	        this.folders = source["folders"];
	        this.systemFolderCount = source["systemFolderCount"];
	        this.systemFileCount = source["systemFileCount"];
	    }
	}
	export class RestoreFailure {
	    path: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new RestoreFailure(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.reason = source["reason"];
	    }
	}
	export class RemoveResult {
	    removedFiles: string[];
	    removedDirs: string[];
	    failed: RestoreFailure[];
	
	    static createFrom(source: any = {}) {
	        return new RemoveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.removedFiles = source["removedFiles"];
	        this.removedDirs = source["removedDirs"];
	        this.failed = this.convertValues(source["failed"], RestoreFailure);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RestoreResult {
	    new: string[];
	    overwritten: string[];
	    skipped: string[];
	    failed: RestoreFailure[];
	
	    static createFrom(source: any = {}) {
	        return new RestoreResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.new = source["new"];
	        this.overwritten = source["overwritten"];
	        this.skipped = source["skipped"];
	        this.failed = this.convertValues(source["failed"], RestoreFailure);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Settings {
	    theme: string;
	    vaultPath: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.vaultPath = source["vaultPath"];
	    }
	}
	export class SnapshotInfo {
	    app: string;
	    createdAt: string;
	    fileCount: number;
	    restorable: number;
	    bytes: number;
	    stale: boolean;
	    vaultPath: string;
	    orphaned: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SnapshotInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.app = source["app"];
	        this.createdAt = source["createdAt"];
	        this.fileCount = source["fileCount"];
	        this.restorable = source["restorable"];
	        this.bytes = source["bytes"];
	        this.stale = source["stale"];
	        this.vaultPath = source["vaultPath"];
	        this.orphaned = source["orphaned"];
	    }
	}
	
	export class UndoInfo {
	    available: boolean;
	    createdAt: string;
	    fileCount: number;
	    restorable: number;
	    stale: boolean;
	    vaultPath: string;
	
	    static createFrom(source: any = {}) {
	        return new UndoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.createdAt = source["createdAt"];
	        this.fileCount = source["fileCount"];
	        this.restorable = source["restorable"];
	        this.stale = source["stale"];
	        this.vaultPath = source["vaultPath"];
	    }
	}
	export class UndoResult {
	    restored: string[];
	    failed: RestoreFailure[];
	
	    static createFrom(source: any = {}) {
	        return new UndoResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.restored = source["restored"];
	        this.failed = this.convertValues(source["failed"], RestoreFailure);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UntrackPreview {
	    dir: string;
	    keepsTracked: number;
	    stopsTracking: number;
	
	    static createFrom(source: any = {}) {
	        return new UntrackPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.keepsTracked = source["keepsTracked"];
	        this.stopsTracking = source["stopsTracking"];
	    }
	}
	export class UpdateResult {
	    updated: string[];
	    skipped: string[];
	    missing: string[];
	    blocked: string[];
	
	    static createFrom(source: any = {}) {
	        return new UpdateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.updated = source["updated"];
	        this.skipped = source["skipped"];
	        this.missing = source["missing"];
	        this.blocked = source["blocked"];
	    }
	}
	export class VaultDirStatus {
	    path: string;
	    set: boolean;
	    reachable: boolean;
	    manifestReadable: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new VaultDirStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.set = source["set"];
	        this.reachable = source["reachable"];
	        this.manifestReadable = source["manifestReadable"];
	        this.message = source["message"];
	    }
	}
	export class VaultProbe {
	    hasManifest: boolean;
	    isEmpty: boolean;
	    appCount: number;
	    entryCount: number;
	    needsAdmin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new VaultProbe(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasManifest = source["hasManifest"];
	        this.isEmpty = source["isEmpty"];
	        this.appCount = source["appCount"];
	        this.entryCount = source["entryCount"];
	        this.needsAdmin = source["needsAdmin"];
	    }
	}

}

