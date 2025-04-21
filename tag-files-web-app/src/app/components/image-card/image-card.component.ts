import { Component, Input, } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { FileMetadata } from '../../model/file-metadata';

@Component({
  selector: 'app-image-card',
  imports: [MatCardModule, MatButtonModule],
  templateUrl: './image-card.component.html',
  styleUrl: './image-card.component.scss'
})
export class ImageCardComponent {
  @Input() metadata!: FileMetadata;
}
