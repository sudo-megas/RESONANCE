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

