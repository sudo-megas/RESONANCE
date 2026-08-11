export namespace main {
	
	export class FileRow {
	    path: string;
	    state: string;
	    sourceModified: string;
	    vaultModified: string;
	
	    static createFrom(source: any = {}) {
	        return new FileRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.state = source["state"];
	        this.sourceModified = source["sourceModified"];
	        this.vaultModified = source["vaultModified"];
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
	export class UndoInfo {
	    available: boolean;
	    createdAt: string;
	    fileCount: number;
	
	    static createFrom(source: any = {}) {
	        return new UndoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.createdAt = source["createdAt"];
	        this.fileCount = source["fileCount"];
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
	export class UpdateResult {
	    updated: string[];
	    skipped: string[];
	    missing: string[];
	
	    static createFrom(source: any = {}) {
	        return new UpdateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.updated = source["updated"];
	        this.skipped = source["skipped"];
	        this.missing = source["missing"];
	    }
	}
	export class VaultProbe {
	    hasManifest: boolean;
	    isEmpty: boolean;
	    appCount: number;
	
	    static createFrom(source: any = {}) {
	        return new VaultProbe(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasManifest = source["hasManifest"];
	        this.isEmpty = source["isEmpty"];
	        this.appCount = source["appCount"];
	    }
	}

}

