export namespace domain {
	
	export class SessionKey {
	    HostID: string;
	    Name: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.HostID = source["HostID"];
	        this.Name = source["Name"];
	    }
	}
	export class Pane {
	    ID: string;
	    WindowID: string;
	    SessionKey: SessionKey;
	    Active: boolean;
	    Dead: boolean;
	    X: number;
	    Y: number;
	    Width: number;
	    Height: number;
	    Command: string;
	
	    static createFrom(source: any = {}) {
	        return new Pane(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.WindowID = source["WindowID"];
	        this.SessionKey = this.convertValues(source["SessionKey"], SessionKey);
	        this.Active = source["Active"];
	        this.Dead = source["Dead"];
	        this.X = source["X"];
	        this.Y = source["Y"];
	        this.Width = source["Width"];
	        this.Height = source["Height"];
	        this.Command = source["Command"];
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
	export class Window {
	    ID: string;
	    SessionKey: SessionKey;
	    Name: string;
	    Layout: string;
	    Active: boolean;
	    Panes: Record<string, Pane>;
	
	    static createFrom(source: any = {}) {
	        return new Window(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.SessionKey = this.convertValues(source["SessionKey"], SessionKey);
	        this.Name = source["Name"];
	        this.Layout = source["Layout"];
	        this.Active = source["Active"];
	        this.Panes = this.convertValues(source["Panes"], Pane, true);
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
	export class Session {
	    Key: SessionKey;
	    ID: string;
	    Windows: Record<string, Window>;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Key = this.convertValues(source["Key"], SessionKey);
	        this.ID = source["ID"];
	        this.Windows = this.convertValues(source["Windows"], Window, true);
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
	

}

