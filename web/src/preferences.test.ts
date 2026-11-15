import {it,expect} from 'vitest';
import {readPreferences,savePreferences,defaultPreferences} from './preferences';
it('accepts versioned preferences only and survives corrupt or unavailable storage',()=>{
 localStorage.clear();expect(readPreferences(localStorage)).toEqual(defaultPreferences);
 localStorage.setItem('tag-files.preferences',JSON.stringify({version:77,view:'list',density:99,sidebarOpen:false}));expect(readPreferences(localStorage)).toEqual(defaultPreferences);
 savePreferences({view:'list',density:3,sidebarOpen:false},localStorage);expect(readPreferences(localStorage)).toEqual({view:'list',density:3,sidebarOpen:false});
 const broken={getItem(){throw Error('denied');},setItem(){throw Error('denied');}};
 expect(readPreferences(broken)).toEqual(defaultPreferences);expect(()=>savePreferences(defaultPreferences,broken)).not.toThrow();
});
