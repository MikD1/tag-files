import '@testing-library/jest-dom/vitest';
import {afterEach} from 'vitest';
import {cleanup} from '@testing-library/react';
afterEach(cleanup);
if(typeof window!=='undefined'){
 Object.defineProperty(window,'matchMedia',{value:(query:string)=>({matches:false,media:query,addEventListener(){},removeEventListener(){},addListener(){},removeListener(){},dispatchEvent(){return true;}})});
 globalThis.ResizeObserver=class {observe(){} unobserve(){} disconnect(){}};
 Element.prototype.scrollIntoView=function(){};
}
