using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing.Contracts;

public record ProcessingFileDto(
    uint Id,
    string OriginalFileName,
    string LibraryFileName,
    string ContentType,
    ProcessingStatus Status)
{
    public static ProcessingFileDto FromModel(ProcessingFile model)
    {
        return new(
            model.Id,
            model.OriginalFileName,
            model.LibraryFileName,
            model.ContentType,
            model.Status);
    }
}