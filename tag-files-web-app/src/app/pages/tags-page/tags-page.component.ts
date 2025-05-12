import {Component} from '@angular/core';
import {TagsTableComponent} from '../../components/tags-table/tags-table.component';

@Component({
  selector: 'app-tags-page',
  imports: [
    TagsTableComponent
  ],
  templateUrl: './tags-page.component.html',
  styleUrl: './tags-page.component.scss'
})
export class TagsPageComponent {
}
