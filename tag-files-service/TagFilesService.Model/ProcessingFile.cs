namespace TagFilesService.Model;

public class ProcessingFile
{
    public ProcessingFile(string name)
        : this(0u, name, ProcessingStatus.New, DateTime.UtcNow)
    {
    }

    public uint Id { get; private set; }

    public string Name { get; private set; }

    public ProcessingStatus Status { get; private set; }

    public DateTime UploadedOn { get; private set; }

    private ProcessingFile(uint id, string name, ProcessingStatus status, DateTime uploadedOn)
    {
        Id = id;
        Name = name;
        Status = status;
        UploadedOn = uploadedOn;
    }
}