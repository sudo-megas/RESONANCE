export namespace main {
	
	export class ManifestFile {
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ManifestFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	    }
	}
	export class ManifestApp {
	    name: string;
	    files: ManifestFile[];
	
	    static createFrom(source: any = {}) {
	        return new ManifestApp(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.files = this.convertValues(source["files"], ManifestFile);
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

