import {useRef,useState} from 'react';
import {Link} from 'react-router';
import {Modal} from '@mantine/core';
import type {Schema} from '../api/client';
import {Button} from '../components/Button';
import {libraryURL,readLibrary} from './url';

export function TagStatisticsTable({statistics,tags,onEdit}:{statistics:Schema<'TagStatistic'>[];tags:Schema<'Tag'>[];onEdit:(tag:Schema<'Tag'>)=>void}){
 const [choices,setChoices]=useState<Schema<'Tag'>[]>([]);
 const trigger=useRef<HTMLButtonElement|null>(null);
 return <><table className="support-table tags-table"><thead><tr><th>Tag</th><th>Usage count</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{statistics.map(statistic=><tr key={statistic.name}><td><Link className="tag support-link" to={libraryURL({...readLibrary(new URL('/library',location.origin)),q:statistic.name})}>{statistic.name}</Link></td><td>{statistic.usageCount.toLocaleString('en-US')}</td><td><Button icon="edit" label={`Edit ${statistic.name}`} className="ghost icon-button" onClick={event=>{
  trigger.current=event.currentTarget;
  const matching=tags.filter(tag=>tag.name===statistic.name);
  if(matching.length===1)onEdit(matching[0]!);else setChoices(matching);
 }}/></td></tr>)}</tbody></table>
 <Modal opened={choices.length>0} onClose={()=>setChoices([])} title="Choose tag to rename" centered><p>These entries share a name and remain separate tags.</p>{choices.map(tag=><Button key={tag.id} className="secondary" onClick={()=>{setChoices([]);trigger.current?.focus();onEdit(tag);}}>Tag ID {tag.id}</Button>)}</Modal></>;
}
